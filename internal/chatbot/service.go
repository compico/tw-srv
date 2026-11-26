package chatbot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/compico/tw-srv/internal/memealert"
	"github.com/compico/tw-srv/internal/platform/httpx"
	"github.com/compico/tw-srv/internal/platform/secretbox"
	"github.com/compico/tw-srv/internal/twitch"
)

var (
	ErrDisabled = errors.New("chatbot: second Twitch app is not configured")
	ErrBadScope = errors.New("chatbot: token has no user:write:chat scope")
)

const (
	maxPending   = 10                      // сообщений в очереди на одного стримера; остальные отбрасываются
	sendInterval = 1500 * time.Millisecond // пауза между сообщениями одного стримера
	refreshSkew  = 5 * time.Minute
)

// ValidationError: некорректные шаблоны.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

type Service struct {
	repo  *Repository
	box   *secretbox.Box
	oauth *twitch.OAuth // nil: функция выключена (нет второго Twitch-приложения)
	helix *twitch.Helix
	log   *slog.Logger

	guideURL string // адрес страницы-гайда для подстановки {guide}

	locks   sync.Map // streamerID -> *sync.Mutex (отправка и обновление токена)
	pending sync.Map // streamerID -> *atomic.Int32
}

func NewService(repo *Repository, box *secretbox.Box, oauth *twitch.OAuth, helix *twitch.Helix, publicURL string, log *slog.Logger) *Service {
	return &Service{repo: repo, box: box, oauth: oauth, helix: helix, log: log, guideURL: strings.TrimRight(publicURL, "/") + "/guide/id"}
}

func (s *Service) Enabled() bool { return s.oauth != nil }

func lockFor(m *sync.Map, key string) *sync.Mutex {
	v, _ := m.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ---------------------------------------------------------------- settings

type Template struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"`
}

type Templates struct {
	Success Template `json:"success"`
	Fail    Template `json:"fail"`
	// Duplicate: ник есть у нескольких саппортеров. Если выключено, в этом случае уходит сообщение о неудаче.
	Duplicate Template `json:"duplicate"`
}

func (s *Service) Templates(ctx context.Context, streamerID string) (Templates, error) {
	st, err := s.repo.GetSettings(ctx, streamerID)
	if err != nil {
		return Templates{}, err
	}
	if st == nil {
		return Templates{
			Success:   Template{Text: DefaultSuccessText},
			Fail:      Template{Text: DefaultFailText},
			Duplicate: Template{Text: DefaultDuplicateText},
		}, nil
	}
	dup := Template{Enabled: st.DuplicateEnabled, Text: st.DuplicateText}
	if dup.Text == "" { // строка создана до появления этого сообщения
		dup.Text = DefaultDuplicateText
	}
	return Templates{
		Success:   Template{Enabled: st.SuccessEnabled, Text: st.SuccessText},
		Fail:      Template{Enabled: st.FailEnabled, Text: st.FailText},
		Duplicate: dup,
	}, nil
}

func normalizeTemplate(field string, t Template) (Template, error) {
	t.Text = strings.TrimSpace(t.Text)
	if utf8.RuneCountInString(t.Text) > MaxTemplateRunes {
		return t, &ValidationError{field, fmt.Sprintf("Не длиннее %d символов", MaxTemplateRunes)}
	}
	if t.Enabled && t.Text == "" {
		return t, &ValidationError{field, "Введите текст сообщения или выключите его"}
	}
	return t, nil
}

func (s *Service) SaveTemplates(ctx context.Context, streamerID string, in Templates) (Templates, error) {
	var err error
	if in.Success, err = normalizeTemplate("success", in.Success); err != nil {
		return in, err
	}
	if in.Fail, err = normalizeTemplate("fail", in.Fail); err != nil {
		return in, err
	}
	if in.Duplicate, err = normalizeTemplate("duplicate", in.Duplicate); err != nil {
		return in, err
	}
	err = s.repo.SaveSettings(ctx, &Settings{
		TwitchUserID:     streamerID,
		SuccessEnabled:   in.Success.Enabled,
		SuccessText:      in.Success.Text,
		FailEnabled:      in.Fail.Enabled,
		FailText:         in.Fail.Text,
		DuplicateEnabled: in.Duplicate.Enabled,
		DuplicateText:    in.Duplicate.Text,
		UpdatedAt:        time.Now(),
	})
	return in, err
}

// ---------------------------------------------------------------- connect

// ConnectTTL: сколько живёт ссылка подключения бота.
const ConnectTTL = 10 * time.Minute

var ErrBadState = errors.New("chatbot: invalid or expired state")

// BeginConnect создаёт одноразовую ссылку авторизации Twitch для бота стримера streamerID.
// Ссылку можно открыть в любом браузере: личность стримера хранится в состоянии на сервере.
func (s *Service) BeginConnect(ctx context.Context, streamerID string) (string, error) {
	if s.oauth == nil {
		return "", ErrDisabled
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := hex.EncodeToString(raw)
	if err := s.repo.SaveState(ctx, &ConnectState{State: state, TwitchUserID: streamerID, ExpiresAt: time.Now().Add(ConnectTTL)}); err != nil {
		return "", err
	}
	return s.oauth.AuthorizeURLForceVerify(state, []string{Scope}), nil
}

// CompleteConnect обменивает code на токен бота и сохраняет его (в зашифрованном виде) для стримера,
// создавшего ссылку.
func (s *Service) CompleteConnect(ctx context.Context, state, code string) error {
	if s.oauth == nil {
		return ErrDisabled
	}
	st, err := s.repo.TakeState(ctx, state)
	if err != nil || time.Now().After(st.ExpiresAt) {
		return ErrBadState
	}
	streamerID := st.TwitchUserID
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return err
	}
	if !hasScope(tok.Scope, Scope) {
		return ErrBadScope
	}
	user, err := s.oauth.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		return err
	}
	accessEnc, err := s.box.Seal(tok.AccessToken)
	if err != nil {
		return err
	}
	refreshEnc, err := s.box.Seal(tok.RefreshToken)
	if err != nil {
		return err
	}
	return s.repo.SaveBot(ctx, &Bot{
		TwitchUserID:    streamerID,
		BotUserID:       user.ID,
		BotLogin:        user.Login,
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		ExpiresAt:       time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
	})
}

func hasScope(scopes []string, want string) bool {
	for _, sc := range scopes {
		if sc == want {
			return true
		}
	}
	return false
}

func (s *Service) Bot(ctx context.Context, streamerID string) (*Bot, error) {
	return s.repo.GetBot(ctx, streamerID)
}

// Disconnect удаляет токен бота и отзывает его на стороне Twitch (отзыв не критичен).
func (s *Service) Disconnect(ctx context.Context, streamerID string) error {
	b, err := s.repo.GetBot(ctx, streamerID)
	if err != nil || b == nil {
		return err
	}
	if err := s.repo.DeleteBot(ctx, streamerID); err != nil {
		return err
	}
	if s.oauth != nil {
		if access, err := s.box.Open(b.AccessTokenEnc); err == nil {
			if err := s.oauth.Revoke(ctx, access); err != nil {
				s.log.Info("chatbot: revoke token", "streamer", streamerID, "err", err)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- sending

// Notify реализует memealert.ChatNotifier: не блокирует и никогда не возвращает ошибку.
func (s *Service) Notify(ev memealert.ChatEvent) {
	if s.oauth == nil {
		return
	}
	v, _ := s.pending.LoadOrStore(ev.StreamerID, new(atomic.Int32))
	cnt := v.(*atomic.Int32)
	if cnt.Add(1) > maxPending {
		cnt.Add(-1)
		s.log.Warn("chatbot: queue full, message dropped", "streamer", ev.StreamerID)
		return
	}
	go func() {
		defer cnt.Add(-1)
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("chatbot: panic", "recover", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		s.deliver(ctx, ev)
	}()
}

func (s *Service) deliver(ctx context.Context, ev memealert.ChatEvent) {
	st, err := s.repo.GetSettings(ctx, ev.StreamerID)
	if err != nil || st == nil {
		if err != nil {
			s.log.Warn("chatbot: load settings", "streamer", ev.StreamerID, "err", err)
		}
		return
	}
	tpl, on := st.FailText, st.FailEnabled
	switch {
	case ev.Success:
		tpl, on = st.SuccessText, st.SuccessEnabled
	case ev.Duplicate && st.DuplicateEnabled && st.DuplicateText != "":
		tpl, on = st.DuplicateText, true
	}
	ev.GuideURL = s.guideURL
	if !on {
		return
	}
	msg := Render(tpl, ev)
	if msg == "" {
		return
	}

	mu := lockFor(&s.locks, ev.StreamerID)
	mu.Lock()
	defer mu.Unlock()

	b, err := s.repo.GetBot(ctx, ev.StreamerID)
	if err != nil {
		s.log.Warn("chatbot: load bot", "streamer", ev.StreamerID, "err", err)
		return
	}
	if b == nil || b.Invalid {
		return
	}
	if err := s.send(ctx, b, ev.StreamerID, msg); err != nil {
		s.log.Warn("chatbot: send failed", "streamer", ev.StreamerID, "err", err)
		_ = s.repo.UpdateBot(ctx, ev.StreamerID, map[string]any{"last_error": errText(err), "last_error_at": time.Now()})
	} else if b.LastError != "" {
		_ = s.repo.UpdateBot(ctx, ev.StreamerID, map[string]any{"last_error": ""})
	}
	select { // пауза, чтобы пачка исходов не превратилась в спам и не упёрлась в лимиты Twitch
	case <-ctx.Done():
	case <-time.After(sendInterval):
	}
}

func (s *Service) send(ctx context.Context, b *Bot, streamerID, msg string) error {
	access, err := s.accessToken(ctx, b, false)
	if err != nil {
		return err
	}
	res, err := s.sendOnce(ctx, access, b, streamerID, msg)
	if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusUnauthorized {
		// Токен мог истечь раньше срока: обновляем один раз и повторяем.
		if access, err = s.accessToken(ctx, b, true); err != nil {
			return err
		}
		res, err = s.sendOnce(ctx, access, b, streamerID, msg)
	}
	if err != nil {
		return err
	}
	if !res.IsSent {
		reason := "сообщение отклонено Twitch"
		if res.DropReason != nil && res.DropReason.Message != "" {
			reason += ": " + res.DropReason.Message
		}
		return errors.New(reason)
	}
	return nil
}

func (s *Service) sendOnce(ctx context.Context, access string, b *Bot, streamerID, msg string) (*twitch.ChatMessageResult, error) {
	creds := twitch.Credentials{AccessToken: access, ClientID: s.oauth.ClientID()}
	return s.helix.SendChatMessage(ctx, creds, streamerID, b.BotUserID, msg)
}

// accessToken возвращает действующий токен, при необходимости обновляя его.
// Вызывается под блокировкой стримера.
func (s *Service) accessToken(ctx context.Context, b *Bot, force bool) (string, error) {
	if !force && time.Until(b.ExpiresAt) > refreshSkew {
		return s.box.Open(b.AccessTokenEnc)
	}
	refresh, err := s.box.Open(b.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	tok, err := s.oauth.Refresh(ctx, refresh)
	if err != nil {
		if errors.Is(err, twitch.ErrInvalidGrant) {
			b.Invalid = true
			_ = s.repo.UpdateBot(ctx, b.TwitchUserID, map[string]any{"invalid": true})
		}
		return "", err
	}
	accessEnc, err := s.box.Seal(tok.AccessToken)
	if err != nil {
		return "", err
	}
	refreshEnc, err := s.box.Seal(tok.RefreshToken)
	if err != nil {
		return "", err
	}
	b.AccessTokenEnc, b.RefreshTokenEnc = accessEnc, refreshEnc
	b.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if err := s.repo.UpdateBot(ctx, b.TwitchUserID, map[string]any{
		"access_token_enc": accessEnc, "refresh_token_enc": refreshEnc, "expires_at": b.ExpiresAt,
	}); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func errText(err error) string {
	msg := err.Error()
	if se, ok := httpx.AsStatus(err); ok {
		msg = fmt.Sprintf("Twitch ответил %d", se.Code)
	}
	if utf8.RuneCountInString(msg) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return msg
}
