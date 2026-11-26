package chatbot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/compico/tw-srv/internal/memealert"
	"github.com/compico/tw-srv/internal/platform/sqlitedb"
	"github.com/compico/tw-srv/internal/twitch"
)

func TestRender(t *testing.T) {
	ev := memealert.ChatEvent{ViewerName: "Вася", ViewerLogin: "vasya", Nick: "bob", Amount: 10, Reason: "ник не найден"}
	got := Render("@{user}, {amount} мемкоинов для {nick}. {reason}", ev)
	want := "@Вася, 10 мемкоинов для bob. ник не найден"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderFallsBackToLogin(t *testing.T) {
	got := Render("{user}", memealert.ChatEvent{ViewerLogin: "vasya"})
	if got != "vasya" {
		t.Fatalf("got %q", got)
	}
}

// Значения от зрителя не должны ломать сообщение: переводы строк, команды, слишком длинный ник.
func TestRenderSanitizesViewerInput(t *testing.T) {
	ev := memealert.ChatEvent{ViewerName: "a\nb\r\x00c", Nick: strings.Repeat("я", 500)}
	got := Render("{user} {nick}", ev)
	if strings.ContainsAny(got, "\n\r\x00") {
		t.Fatalf("управляющие символы остались: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 40+1+40 {
		t.Fatalf("значения не обрезаны: %d", n)
	}

	got = Render("{nick}", memealert.ChatEvent{Nick: "/ban someone"})
	if strings.HasPrefix(got, "/") || strings.HasPrefix(got, ".") {
		t.Fatalf("сообщение не должно начинаться с команды: %q", got)
	}
}

func TestRenderLimit(t *testing.T) {
	got := Render(strings.Repeat("ы", 450)+"{user}{user}{user}{user}", memealert.ChatEvent{ViewerName: strings.Repeat("д", 40)})
	if utf8.RuneCountInString(got) > MaxMessageRunes {
		t.Fatalf("длина %d больше лимита Twitch", utf8.RuneCountInString(got))
	}
}

func TestRenderUnknownVariableStays(t *testing.T) {
	if got := Render("{unknown} {user}", memealert.ChatEvent{ViewerName: "x"}); got != "{unknown} x" {
		t.Fatalf("got %q", got)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(repo, nil, nil, nil, nilLogger())
}

func TestTemplatesDefaultOff(t *testing.T) {
	s := newTestService(t)
	tpl, err := s.Templates(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Success.Enabled || tpl.Fail.Enabled {
		t.Fatal("по умолчанию сообщения должны быть выключены")
	}
	if tpl.Success.Text != DefaultSuccessText || tpl.Fail.Text != DefaultFailText {
		t.Fatalf("неожиданные тексты по умолчанию: %+v", tpl)
	}
}

func TestSaveTemplatesRoundTrip(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	in := Templates{Success: Template{Enabled: true, Text: "  ок {user} "}, Fail: Template{Enabled: false, Text: "не ок"}}
	if _, err := s.SaveTemplates(ctx, "u1", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Templates(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Success.Enabled || got.Success.Text != "ок {user}" || got.Fail.Enabled || got.Fail.Text != "не ок" {
		t.Fatalf("got %+v", got)
	}
	other, _ := s.Templates(ctx, "u2")
	if other.Success.Enabled {
		t.Fatal("настройки не должны делиться между стримерами")
	}
}

func TestSaveTemplatesValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	_, err := s.SaveTemplates(ctx, "u1", Templates{Success: Template{Enabled: true, Text: "  "}})
	if ve, ok := err.(*ValidationError); !ok || ve.Field != "success" {
		t.Fatalf("ожидали ошибку поля success, got %v", err)
	}
	_, err = s.SaveTemplates(ctx, "u1", Templates{Fail: Template{Text: strings.Repeat("я", MaxTemplateRunes+1)}})
	if ve, ok := err.(*ValidationError); !ok || ve.Field != "fail" {
		t.Fatalf("ожидали ошибку поля fail, got %v", err)
	}
	// Выключенный пустой шаблон допустим.
	if _, err := s.SaveTemplates(ctx, "u1", Templates{}); err != nil {
		t.Fatal(err)
	}
}

// Без второго Twitch-приложения функция выключена, а уведомления тихо игнорируются.
func TestDisabledService(t *testing.T) {
	s := newTestService(t)
	if s.Enabled() {
		t.Fatal("без oauth функция должна быть выключена")
	}
	s.Notify(memealert.ChatEvent{StreamerID: "u1", Success: true}) // не должно паниковать и блокироваться
	if _, err := s.BeginConnect(context.Background(), "u1"); err != ErrDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestBotRepository(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if b, err := s.repo.GetBot(ctx, "u1"); err != nil || b != nil {
		t.Fatalf("пустая таблица: %v %v", b, err)
	}
	if err := s.repo.SaveBot(ctx, &Bot{TwitchUserID: "u1", BotUserID: "b1", BotLogin: "mybot"}); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.UpdateBot(ctx, "u1", map[string]any{"invalid": true, "last_error": "x"}); err != nil {
		t.Fatalf("имена колонок должны совпадать с моделью: %v", err)
	}
	b, _ := s.repo.GetBot(ctx, "u1")
	if b == nil || !b.Invalid || b.LastError != "x" || b.BotLogin != "mybot" {
		t.Fatalf("got %+v", b)
	}
	if err := s.Disconnect(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.repo.GetBot(ctx, "u1"); b != nil {
		t.Fatal("бот должен быть удалён")
	}
}

func nilLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newEnabledService(t *testing.T) *Service {
	t.Helper()
	s := newTestService(t)
	s.oauth = twitch.NewOAuth("bot-app", "secret", "https://example.com/api/bot/callback")
	return s
}

func TestBeginConnectCreatesOneTimeState(t *testing.T) {
	s := newEnabledService(t)
	ctx := context.Background()
	link, err := s.BeginConnect(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "bot-app" || q.Get("scope") != Scope || q.Get("force_verify") != "true" {
		t.Fatalf("неожиданная ссылка: %s", link)
	}
	state := q.Get("state")
	if len(state) < 32 {
		t.Fatalf("state слишком короткий: %q", state)
	}
	st, err := s.repo.TakeState(ctx, state)
	if err != nil || st.TwitchUserID != "u1" {
		t.Fatalf("take: %+v %v", st, err)
	}
	if _, err := s.repo.TakeState(ctx, state); err == nil {
		t.Fatal("ссылка должна работать один раз")
	}
}

func TestCompleteConnectRejectsUnknownAndExpiredState(t *testing.T) {
	s := newEnabledService(t)
	ctx := context.Background()
	if err := s.CompleteConnect(ctx, "nope", "code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("неизвестный state: %v", err)
	}
	if err := s.repo.SaveState(ctx, &ConnectState{State: "old", TwitchUserID: "u1", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteConnect(ctx, "old", "code"); !errors.Is(err, ErrBadState) {
		t.Fatalf("просроченный state: %v", err)
	}
	if b, _ := s.repo.GetBot(ctx, "u1"); b != nil {
		t.Fatal("бот не должен подключаться по просроченной ссылке")
	}
}
