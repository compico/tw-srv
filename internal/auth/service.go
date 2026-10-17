package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/compico/tw-srv/internal/platform/secretbox"
	"github.com/compico/tw-srv/internal/twitch"
)

var (
	ErrBadState     = errors.New("auth: invalid or expired login state")
	ErrMissingScope = errors.New("auth: required scope was not granted")
)

type Config struct {
	Scopes        []string
	SessionTTL    time.Duration
	RefreshSkew   time.Duration // обновлять токен, если до истечения осталось меньше
	ValidateEvery time.Duration // как часто звать /oauth2/validate (Twitch просит раз в час)
}

type Service struct {
	repo  *Repository
	tw    *twitch.OAuth
	box   *secretbox.Box
	cfg   Config
	log   *slog.Logger
	locks sync.Map // userID -> *sync.Mutex
}

func NewService(repo *Repository, tw *twitch.OAuth, box *secretbox.Box, cfg Config, log *slog.Logger) *Service {
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 5 * time.Minute
	}
	if cfg.ValidateEvery <= 0 {
		cfg.ValidateEvery = time.Hour
	}
	return &Service{repo: repo, tw: tw, box: box, cfg: cfg, log: log}
}

// SafeReturnTo пропускает только локальные пути (защита от open redirect).
func SafeReturnTo(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func (s *Service) lock(userID string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// BeginLogin готовит редирект на Twitch. state нужно положить ещё и в cookie.
func (s *Service) BeginLogin(ctx context.Context, returnTo string) (authURL, state string, err error) {
	state, err = randomToken(24)
	if err != nil {
		return "", "", err
	}
	ls := &LoginState{State: state, ReturnTo: SafeReturnTo(returnTo), ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err := s.repo.SaveState(ctx, ls); err != nil {
		return "", "", err
	}
	return s.tw.AuthorizeURL(state, s.cfg.Scopes), state, nil
}

// CompleteLogin обменивает code на токены, создаёт пользователя и сессию.
func (s *Service) CompleteLogin(ctx context.Context, code, state string) (sessionID, returnTo string, err error) {
	ls, err := s.repo.TakeState(ctx, state)
	if err != nil || time.Now().After(ls.ExpiresAt) {
		return "", "", ErrBadState
	}

	tok, err := s.tw.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("exchange code: %w", err)
	}
	for _, need := range s.cfg.Scopes {
		if !slices.Contains(tok.Scope, need) {
			return "", "", fmt.Errorf("%w: %s", ErrMissingScope, need)
		}
	}

	info, err := s.tw.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		return "", "", fmt.Errorf("fetch twitch user: %w", err)
	}
	user := &User{ID: info.ID, Login: info.Login, DisplayName: info.DisplayName, AvatarURL: info.ProfileImageURL}
	if err := s.repo.UpsertUser(ctx, user); err != nil {
		return "", "", err
	}

	mu := s.lock(user.ID)
	mu.Lock()
	err = s.storeToken(ctx, user.ID, tok, "")
	mu.Unlock()
	if err != nil {
		return "", "", err
	}

	sessionID, err = s.newSession(ctx, user.ID)
	if err != nil {
		return "", "", err
	}
	return sessionID, ls.ReturnTo, nil
}

func (s *Service) newSession(ctx context.Context, userID string) (string, error) {
	id, err := randomToken(32)
	if err != nil {
		return "", err
	}
	sess := &Session{IDHash: hashID(id), UserID: userID, ExpiresAt: time.Now().Add(s.cfg.SessionTTL)}
	if err := s.repo.CreateSession(ctx, sess); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) ValidateSession(ctx context.Context, sessionID string) (*User, error) {
	if sessionID == "" {
		return nil, twitch.ErrNoSession
	}
	sess, err := s.repo.GetSession(ctx, hashID(sessionID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, twitch.ErrNoSession
		}
		return nil, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.repo.DeleteSession(ctx, sess.IDHash)
		return nil, twitch.ErrNoSession
	}
	u, err := s.repo.GetUser(ctx, sess.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, twitch.ErrNoSession
		}
		return nil, err
	}
	return u, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, hashID(sessionID))
}

// AccessGrant: актуальный токен для вызовов Helix.
type AccessGrant struct {
	AccessToken string
	ClientID    string
	ExpiresAt   time.Time
	Scopes      []string
}

// AccessToken отдаёт рабочий access token, при необходимости обновляя его.
// Обновление идёт под блокировкой пользователя: refresh token у Twitch может ротироваться,
// и два параллельных обновления не должны затирать друг друга.
func (s *Service) AccessToken(ctx context.Context, userID string) (*AccessGrant, error) {
	mu := s.lock(userID)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.repo.GetToken(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, twitch.ErrUnknownUser
		}
		return nil, err
	}
	if t.Invalid {
		return nil, twitch.ErrReauthRequired
	}
	if time.Until(t.ExpiresAt) <= s.cfg.RefreshSkew {
		t, err = s.refreshLocked(ctx, t)
		if err != nil {
			return nil, err
		}
	}
	return s.grant(t)
}

func (s *Service) grant(t *Token) (*AccessGrant, error) {
	access, err := s.box.Open(t.AccessTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}
	return &AccessGrant{
		AccessToken: access,
		ClientID:    s.tw.ClientID(),
		ExpiresAt:   t.ExpiresAt,
		Scopes:      strings.Fields(t.Scopes),
	}, nil
}

// refreshLocked вызывать только под lock(userID).
func (s *Service) refreshLocked(ctx context.Context, t *Token) (*Token, error) {
	refresh, err := s.box.Open(t.RefreshTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh token: %w", err)
	}
	resp, err := s.tw.Refresh(ctx, refresh)
	if err != nil {
		if errors.Is(err, twitch.ErrInvalidGrant) {
			s.log.Warn("twitch refresh token rejected, reauth required", "user", t.UserID)
			_ = s.repo.MarkTokenInvalid(ctx, t.UserID, "refresh token rejected by Twitch")
			return nil, twitch.ErrReauthRequired
		}
		// Сетевая ошибка или сбой Twitch: токен не помечаем невалидным.
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	if err := s.storeToken(ctx, t.UserID, resp, t.RefreshTokenEnc); err != nil {
		return nil, err
	}
	return s.repo.GetToken(ctx, t.UserID)
}

// storeToken шифрует и сохраняет пару. Если Twitch не вернул новый refresh token,
// остаётся прежний (prevRefreshEnc).
func (s *Service) storeToken(ctx context.Context, userID string, tok *twitch.TokenResponse, prevRefreshEnc string) error {
	accessEnc, err := s.box.Seal(tok.AccessToken)
	if err != nil {
		return err
	}
	refreshEnc := prevRefreshEnc
	if tok.RefreshToken != "" {
		refreshEnc, err = s.box.Seal(tok.RefreshToken)
		if err != nil {
			return err
		}
	}
	now := time.Now()
	scopes := strings.Join(tok.Scope, " ")
	if len(tok.Scope) == 0 && prevRefreshEnc != "" {
		// При refresh Twitch возвращает scope; если нет, сохраним прежний.
		if old, err := s.repo.GetToken(ctx, userID); err == nil {
			scopes = old.Scopes
		}
	}
	return s.repo.SaveToken(ctx, &Token{
		UserID:          userID,
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		Scopes:          scopes,
		ExpiresAt:       now.Add(time.Duration(tok.ExpiresIn) * time.Second),
		LastValidatedAt: now,
		Invalid:         false,
		UpdatedAt:       now,
	})
}

// Run: фоновое обслуживание токенов: упреждающее обновление, почасовой validate,
// чистка просроченных сессий. Блокируется до отмены ctx.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.maintain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maintain(ctx)
		}
	}
}

func (s *Service) maintain(ctx context.Context) {
	now := time.Now()
	due, err := s.repo.TokensDue(ctx, now.Add(s.cfg.RefreshSkew*2), now.Add(-s.cfg.ValidateEvery))
	if err != nil {
		s.log.Error("list due tokens", "err", err)
		return
	}
	for _, t := range due {
		if ctx.Err() != nil {
			return
		}
		s.maintainOne(ctx, t.UserID)
	}
	if err := s.repo.DeleteExpired(ctx, now); err != nil {
		s.log.Warn("cleanup expired", "err", err)
	}
}

func (s *Service) maintainOne(ctx context.Context, userID string) {
	mu := s.lock(userID)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.repo.GetToken(ctx, userID)
	if err != nil || t.Invalid {
		return
	}
	if time.Until(t.ExpiresAt) <= s.cfg.RefreshSkew*2 {
		if _, err := s.refreshLocked(ctx, t); err != nil {
			s.log.Warn("background refresh failed", "user", userID, "err", err)
		}
		return
	}
	access, err := s.box.Open(t.AccessTokenEnc)
	if err != nil {
		s.log.Error("decrypt token", "user", userID, "err", err)
		return
	}
	switch _, err := s.tw.Validate(ctx, access); {
	case err == nil:
		_ = s.repo.TouchValidated(ctx, userID, time.Now())
	case errors.Is(err, twitch.ErrInvalidToken):
		// Токен отозван досрочно: пробуем получить новый через refresh.
		if _, err := s.refreshLocked(ctx, t); err != nil {
			s.log.Warn("refresh after failed validate", "user", userID, "err", err)
		}
	default:
		s.log.Warn("validate failed", "user", userID, "err", err)
	}
}
