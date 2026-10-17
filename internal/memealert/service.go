package memealert

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/compico/tw-srv/internal/platform/httpx"
	"github.com/compico/tw-srv/internal/platform/secretbox"
	"github.com/compico/tw-srv/internal/twitch"
)

var (
	ErrNotConnected     = errors.New("memealert: MemeAlerts account is not connected")
	ErrMemeAlertsReauth = errors.New("memealert: MemeAlerts access was revoked, reconnect required")
	ErrBadState         = errors.New("memealert: invalid or expired state")
)

// ValidationError: некорректные настройки награды.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// UserError: понятная пользователю ошибка (например, отказ Twitch).
type UserError struct{ Message string }

func (e *UserError) Error() string { return e.Message }

// Listeners управляет фоновыми слушателями EventSub (реализует Manager).
type Listeners interface {
	Start(userID string) // (пере)запускает слушатель по текущим настройкам награды
	Stop(userID string)
}

type noopListeners struct{}

func (noopListeners) Start(string) {}
func (noopListeners) Stop(string)  {}

var maScopes = []string{"oauth-user-show", "oauth-bonus-give"}

type Service struct {
	repo        *Repository
	ma          *APIClient
	box         *secretbox.Box
	tokens      twitch.TokenSource
	helix       *twitch.Helix
	log         *slog.Logger
	redirectURI string
	listeners   Listeners

	maLocks       sync.Map // twitchUserID -> *sync.Mutex (обновление токена MemeAlerts)
	streamerLocks sync.Map // twitchUserID -> *sync.Mutex (последовательная обработка заявок)
}

func NewService(repo *Repository, ma *APIClient, box *secretbox.Box, tokens twitch.TokenSource, helix *twitch.Helix, publicURL string, log *slog.Logger) *Service {
	return &Service{
		repo:        repo,
		ma:          ma,
		box:         box,
		tokens:      tokens,
		helix:       helix,
		log:         log,
		redirectURI: strings.TrimRight(publicURL, "/") + "/api/memealerts/callback",
		listeners:   noopListeners{},
	}
}

func (s *Service) SetListeners(l Listeners) { s.listeners = l }

func lockFor(m *sync.Map, key string) *sync.Mutex {
	v, _ := m.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Recover вызывается при старте: помечает «зависшие» выдачи и поднимает слушатели.
func (s *Service) Recover(ctx context.Context) error {
	n, err := s.repo.MarkInterruptedGiving(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		s.log.Warn("redemptions interrupted during bonus giving, marked for review", "count", n)
	}
	rewards, err := s.repo.ListActiveRewards(ctx)
	if err != nil {
		return err
	}
	for _, rw := range rewards {
		s.listeners.Start(rw.TwitchUserID)
	}
	s.log.Info("listeners started", "count", len(rewards))
	return nil
}

// MarkTwitchReauth: Twitch отозвал доступ стримера; слушатель остановлен.
func (s *Service) MarkTwitchReauth(ctx context.Context, userID string) {
	if err := s.repo.SetTwitchReauth(ctx, userID, true); err != nil {
		s.log.Error("mark twitch reauth", "user", userID, "err", err)
	}
}

// ---------------------------------------------------------------- MemeAlerts connect

func (s *Service) BeginConnect(ctx context.Context, twitchUserID string) (authURL, state string, err error) {
	state, err = randomToken(24)
	if err != nil {
		return "", "", err
	}
	st := &OAuthState{State: state, TwitchUserID: twitchUserID, ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err := s.repo.SaveOAuthState(ctx, st); err != nil {
		return "", "", err
	}
	return s.ma.AuthorizeURL(s.redirectURI, state, maScopes), state, nil
}

func (s *Service) CompleteConnect(ctx context.Context, id twitch.Identity, state, code string) error {
	st, err := s.repo.TakeOAuthState(ctx, state)
	if err != nil || time.Now().After(st.ExpiresAt) || st.TwitchUserID != id.UserID {
		return ErrBadState
	}
	pair, err := s.ma.ExchangeCode(ctx, code, s.redirectURI)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}
	prof, err := s.ma.Profile(ctx, pair.AccessToken)
	if err != nil {
		return fmt.Errorf("fetch profile: %w", err)
	}
	accessEnc, err := s.box.Seal(pair.AccessToken)
	if err != nil {
		return err
	}
	refreshEnc, err := s.box.Seal(pair.RefreshToken)
	if err != nil {
		return err
	}

	acc, err := s.repo.GetAccount(ctx, id.UserID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		acc = &Account{TwitchUserID: id.UserID}
	}
	acc.TwitchLogin = id.Login
	acc.MemeAlertsUserID = prof.ID
	acc.MemeAlertsName = prof.Name
	acc.MemeAlertsLink = prof.Channel.UniqueLink
	acc.AccessTokenEnc = accessEnc
	acc.RefreshTokenEnc = refreshEnc
	acc.ExpiresAt = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
	acc.TokenRefreshedAt = time.Now()
	acc.Invalid = false
	if err := s.repo.SaveAccount(ctx, acc); err != nil {
		return err
	}
	s.listeners.Start(id.UserID)
	return nil
}

// Disconnect отключает MemeAlerts: награда выключается, слушатель останавливается, токены удаляются.
func (s *Service) Disconnect(ctx context.Context, userID string) error {
	s.listeners.Stop(userID)

	if rw, err := s.repo.GetReward(ctx, userID); err == nil {
		if rw.TwitchRewardID != "" {
			if creds, err := s.tokens.Credentials(ctx, userID); err == nil {
				in := rw.helixInput()
				in.IsEnabled = false
				if _, err := s.helix.UpdateReward(ctx, creds, userID, rw.TwitchRewardID, in); err != nil {
					s.log.Warn("disable reward on disconnect", "user", userID, "err", err)
				}
			}
		}
		rw.Enabled = false
		if err := s.repo.SaveReward(ctx, rw); err != nil {
			return err
		}
	}
	return s.repo.DeleteAccount(ctx, userID)
}

// ---------------------------------------------------------------- MemeAlerts tokens

// maAccessToken возвращает действующий токен MemeAlerts стримера.
// stale != "": предыдущий вызов вернул 401 с этим токеном. Если токен в базе уже другой,
// кто-то обновил его раньше нас и повторно рефрешить не нужно (refresh token одноразовый).
func (s *Service) maAccessToken(ctx context.Context, userID, stale string) (string, error) {
	mu := lockFor(&s.maLocks, userID)
	mu.Lock()
	defer mu.Unlock()

	acc, err := s.repo.GetAccount(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrNotConnected
		}
		return "", err
	}
	if acc.Invalid {
		return "", ErrMemeAlertsReauth
	}
	access, err := s.box.Open(acc.AccessTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt access token: %w", err)
	}
	if (stale == "" || access != stale) && time.Until(acc.ExpiresAt) > 2*time.Minute {
		return access, nil
	}

	return s.refreshMALocked(ctx, acc)
}

// refreshMALocked обменивает refresh token MemeAlerts на новую пару. Вызывать под maLocks[userID].
// Refresh token одноразовый: новый сохраняем сразу, до любых дальнейших вызовов.
func (s *Service) refreshMALocked(ctx context.Context, acc *Account) (string, error) {
	userID := acc.TwitchUserID
	refresh, err := s.box.Open(acc.RefreshTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	pair, err := s.ma.Refresh(ctx, refresh)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			s.log.Warn("memealerts refresh token rejected", "user", userID)
			_ = s.repo.SetAccountInvalid(ctx, userID, true)
			return "", ErrMemeAlertsReauth
		}
		return "", err
	}
	accessEnc, err := s.box.Seal(pair.AccessToken)
	if err != nil {
		return "", err
	}
	refreshEnc, err := s.box.Seal(pair.RefreshToken)
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
	if err := s.repo.UpdateAccountTokens(ctx, userID, accessEnc, refreshEnc, exp); err != nil {
		return "", err
	}
	return pair.AccessToken, nil
}

const (
	// Refresh token MemeAlerts живёт 30 дней и действует только пока им пользуются.
	// Обновляем раз в сутки, чтобы подключение не истекало, даже если наградой давно не пользовались.
	maRefreshEvery = 24 * time.Hour
	maRefreshTick  = 15 * time.Minute
)

// RunTokenRefresher в фоне поддерживает токены MemeAlerts живыми. Блокируется до отмены ctx.
func (s *Service) RunTokenRefresher(ctx context.Context) {
	t := time.NewTicker(maRefreshTick)
	defer t.Stop()
	s.refreshStaleMA(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshStaleMA(ctx)
		}
	}
}

func (s *Service) refreshStaleMA(ctx context.Context) {
	accs, err := s.repo.ListAccountsToRefresh(ctx, time.Now().Add(-maRefreshEvery))
	if err != nil {
		s.log.Error("list memealerts accounts to refresh", "err", err)
		return
	}
	for _, a := range accs {
		if ctx.Err() != nil {
			return
		}
		s.keepAliveMA(ctx, a.TwitchUserID)
	}
}

func (s *Service) keepAliveMA(ctx context.Context, userID string) {
	mu := lockFor(&s.maLocks, userID)
	mu.Lock()
	defer mu.Unlock()

	// Перечитываем под блокировкой: токен мог обновиться, пока мы ждали.
	acc, err := s.repo.GetAccount(ctx, userID)
	if err != nil || acc.Invalid || time.Since(acc.TokenRefreshedAt) < maRefreshEvery {
		return
	}
	if _, err := s.refreshMALocked(ctx, acc); err != nil {
		if errors.Is(err, ErrMemeAlertsReauth) {
			return // уже помечено, стримеру покажем «подключите заново»
		}
		s.log.Warn("background memealerts refresh failed", "user", userID, "err", err)
		return
	}
	s.log.Info("memealerts token refreshed", "user", userID)
}

// callMA выполняет вызов API MemeAlerts от имени стримера; при 401 один раз обновляет токен и повторяет.
func (s *Service) callMA(ctx context.Context, userID string, fn func(access string) error) error {
	access, err := s.maAccessToken(ctx, userID, "")
	if err != nil {
		return err
	}
	err = fn(access)
	if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusUnauthorized {
		access, rerr := s.maAccessToken(ctx, userID, access)
		if rerr != nil {
			return rerr
		}
		return fn(access)
	}
	return err
}

// ---------------------------------------------------------------- Reward

// RewardInput: настройки награды от пользователя.
type RewardInput struct {
	Title               string `json:"title"`
	Prompt              string `json:"prompt"`
	Cost                int    `json:"cost"`
	BonusAmount         int    `json:"bonusAmount"`
	UseUserInput        bool   `json:"useUserInput"`
	BackgroundColor     string `json:"backgroundColor"`
	Enabled             bool   `json:"enabled"`
	CooldownSeconds     int    `json:"cooldownSeconds"`
	MaxPerStream        int    `json:"maxPerStream"`
	MaxPerUserPerStream int    `json:"maxPerUserPerStream"`
}

var colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (in *RewardInput) normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.BackgroundColor = strings.TrimSpace(in.BackgroundColor)

	switch {
	case in.Title == "":
		return &ValidationError{"title", "укажите название награды"}
	case utf8.RuneCountInString(in.Title) > 45:
		return &ValidationError{"title", "название не длиннее 45 символов"}
	case utf8.RuneCountInString(in.Prompt) > 200:
		return &ValidationError{"prompt", "описание не длиннее 200 символов"}
	case in.Cost < 1 || in.Cost > 100_000_000:
		return &ValidationError{"cost", "стоимость должна быть не меньше 1"}
	case in.BonusAmount < 1 || in.BonusAmount > MaxBonus:
		return &ValidationError{"bonusAmount", fmt.Sprintf("бонус от 1 до %d мемкоинов", MaxBonus)}
	case in.BackgroundColor != "" && !colorRe.MatchString(in.BackgroundColor):
		return &ValidationError{"backgroundColor", "цвет в формате #RRGGBB"}
	case in.CooldownSeconds < 0 || in.CooldownSeconds > 604_800:
		return &ValidationError{"cooldownSeconds", "перезарядка от 0 до 7 суток"}
	case in.MaxPerStream < 0 || in.MaxPerStream > 1_000_000:
		return &ValidationError{"maxPerStream", "некорректный лимит за стрим"}
	case in.MaxPerUserPerStream < 0 || in.MaxPerUserPerStream > 1_000_000:
		return &ValidationError{"maxPerUserPerStream", "некорректный лимит на зрителя"}
	}
	return nil
}

// helixInput собирает тело запроса к Twitch.
// should_redemptions_skip_request_queue всегда false: заявка должна оставаться в очереди
// (UNFULFILLED), иначе Twitch не даст вернуть баллы зрителю.
func (r *Reward) helixInput() twitch.RewardInput {
	return twitch.RewardInput{
		Title:                             r.Title,
		Cost:                              r.Cost,
		Prompt:                            r.Prompt,
		IsEnabled:                         r.Enabled,
		BackgroundColor:                   r.BackgroundColor,
		IsUserInputRequired:               r.UseUserInput,
		IsMaxPerStreamEnabled:             r.MaxPerStream > 0,
		MaxPerStream:                      r.MaxPerStream,
		IsMaxPerUserPerStreamEnabled:      r.MaxPerUserPerStream > 0,
		MaxPerUserPerStream:               r.MaxPerUserPerStream,
		IsGlobalCooldownEnabled:           r.CooldownSeconds > 0,
		GlobalCooldownSeconds:             r.CooldownSeconds,
		ShouldRedemptionsSkipRequestQueue: false,
	}
}

// SaveReward создаёт или обновляет награду на канале Twitch и в базе, затем перезапускает слушатель.
func (s *Service) SaveReward(ctx context.Context, userID string, in RewardInput) (*Reward, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	acc, err := s.repo.GetAccount(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotConnected
		}
		return nil, err
	}
	if acc.Invalid {
		return nil, ErrMemeAlertsReauth
	}
	creds, err := s.tokens.Credentials(ctx, userID)
	if err != nil {
		return nil, err
	}

	rw, err := s.repo.GetReward(ctx, userID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		rw = &Reward{TwitchUserID: userID}
	}
	rw.Title = in.Title
	rw.Prompt = in.Prompt
	rw.Cost = in.Cost
	rw.BonusAmount = in.BonusAmount
	rw.UseUserInput = in.UseUserInput
	rw.BackgroundColor = in.BackgroundColor
	rw.Enabled = in.Enabled
	rw.CooldownSeconds = in.CooldownSeconds
	rw.MaxPerStream = in.MaxPerStream
	rw.MaxPerUserPerStream = in.MaxPerUserPerStream

	hin := rw.helixInput()
	if rw.TwitchRewardID != "" {
		_, err := s.helix.UpdateReward(ctx, creds, userID, rw.TwitchRewardID, hin)
		if err != nil {
			if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusNotFound {
				// Награду удалили вручную в панели Twitch: создадим заново.
				rw.TwitchRewardID = ""
			} else {
				return nil, twitchUserError(err)
			}
		}
	}
	if rw.TwitchRewardID == "" {
		created, err := s.helix.CreateReward(ctx, creds, userID, hin)
		if err != nil {
			return nil, twitchUserError(err)
		}
		rw.TwitchRewardID = created.ID
	}

	if err := s.repo.SaveReward(ctx, rw); err != nil {
		return nil, err
	}
	s.listeners.Start(userID)
	return rw, nil
}

// DeleteReward удаляет награду на Twitch и настройки у нас.
func (s *Service) DeleteReward(ctx context.Context, userID string) error {
	rw, err := s.repo.GetReward(ctx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	s.listeners.Stop(userID)
	if rw.TwitchRewardID != "" {
		creds, err := s.tokens.Credentials(ctx, userID)
		if err != nil {
			return err
		}
		if err := s.helix.DeleteReward(ctx, creds, userID, rw.TwitchRewardID); err != nil {
			if se, ok := httpx.AsStatus(err); !ok || se.Code != http.StatusNotFound {
				return twitchUserError(err)
			}
		}
	}
	return s.repo.DeleteReward(ctx, userID)
}

func twitchUserError(err error) error {
	se, ok := httpx.AsStatus(err)
	if !ok {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(se.Body), &body)
	msg := body.Message
	lower := strings.ToLower(msg)

	switch {
	case se.Code == http.StatusUnauthorized:
		return twitch.ErrReauthRequired
	case strings.Contains(msg, "DUPLICATE_REWARD"):
		return &UserError{"Награда с таким названием уже есть на канале. Измените название."}
	case strings.Contains(lower, "partner or affiliate"):
		return &UserError{"Награды за баллы канала доступны только аффилиатам и партнёрам Twitch."}
	case strings.Contains(lower, "different client_id"):
		return &UserError{"Эта награда создана другим приложением и не может быть изменена."}
	case se.Code == http.StatusBadRequest || se.Code == http.StatusForbidden:
		return &UserError{"Twitch отклонил настройки: " + msg}
	}
	return err
}

// ---------------------------------------------------------------- Overview / history

type Overview struct {
	Account *Account
	Reward  *Reward
}

func (s *Service) Overview(ctx context.Context, userID string) (*Overview, error) {
	ov := &Overview{}
	if acc, err := s.repo.GetAccount(ctx, userID); err == nil {
		ov.Account = acc
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if rw, err := s.repo.GetReward(ctx, userID); err == nil {
		ov.Reward = rw
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Стример снова вошёл через Twitch: если токен снова рабочий, возобновляем приём событий.
	if ov.Account != nil && ov.Account.TwitchReauth {
		if _, err := s.tokens.Credentials(ctx, userID); err == nil {
			if err := s.repo.SetTwitchReauth(ctx, userID, false); err == nil {
				ov.Account.TwitchReauth = false
				s.listeners.Start(userID)
			}
		}
	}
	return ov, nil
}

func (s *Service) History(ctx context.Context, userID string, limit int) ([]Redemption, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListRedemptions(ctx, userID, limit)
}
