package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/platform/sqlitedb"
	"github.com/compico/tw-srv/internal/platform/webguard"
	"github.com/compico/tw-srv/internal/twitch"
)

func newService(t *testing.T, withKeys bool) *Service {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Subject: "https://site.example", AdminID: "admin"}
	if withKeys {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			t.Fatal(err)
		}
		cfg.PrivateKey, cfg.PublicKey = priv, pub
	}
	s := NewService(repo, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.allowHTTP = true
	return s
}

// browserSubscription имитирует PushSubscription из браузера: ключи P-256 и auth в base64url.
func browserSubscription(t *testing.T, endpoint string) SubscriptionInput {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	var in SubscriptionInput
	in.Endpoint = endpoint
	in.Keys.P256dh = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	in.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	return in
}

func TestSubscribeValidation(t *testing.T) {
	ctx := context.Background()
	s := newService(t, true)
	good := browserSubscription(t, "https://push.example/abc")

	if err := s.Subscribe(ctx, "admin", good, "UA"); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "admin", good, "UA"); err != nil {
		t.Fatalf("subscribing the same endpoint twice must be idempotent: %v", err)
	}
	if n, _ := s.Count(ctx, "admin"); n != 1 {
		t.Fatalf("expected 1 subscription, got %d", n)
	}

	bad := map[string]SubscriptionInput{
		"http endpoint": browserSubscription(t, "http://push.example/abc"),
		"no endpoint":   browserSubscription(t, ""),
	}
	short := browserSubscription(t, "https://push.example/short")
	short.Keys.Auth = "abc"
	bad["short auth"] = short
	junk := browserSubscription(t, "https://push.example/junk")
	junk.Keys.P256dh = "!!!"
	bad["junk key"] = junk
	for name, in := range bad {
		if err := s.Subscribe(ctx, "admin", in, ""); !errors.Is(err, ErrBadSubscription) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	// Чужой endpoint нельзя отписать от чужого имени.
	if err := s.Unsubscribe(ctx, "someone", good.Endpoint); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Count(ctx, "admin"); n != 1 {
		t.Fatal("unsubscribe must be scoped to the owner")
	}
	if err := s.Unsubscribe(ctx, "admin", good.Endpoint); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Count(ctx, "admin"); n != 0 {
		t.Fatal("subscription must be removed")
	}
}

func TestSubscribeLimitAndDisabled(t *testing.T) {
	ctx := context.Background()
	s := newService(t, true)
	for i := 0; i < maxSubsPerUser; i++ {
		if err := s.Subscribe(ctx, "admin", browserSubscription(t, "https://push.example/"+string(rune('a'+i))), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Subscribe(ctx, "admin", browserSubscription(t, "https://push.example/extra"), ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("device limit: %v", err)
	}

	off := newService(t, false)
	if off.Enabled() {
		t.Fatal("service without VAPID keys must be disabled")
	}
	if err := off.Subscribe(ctx, "admin", browserSubscription(t, "https://push.example/x"), ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	off.NotifyStaff("t", "b", "/x") // не должно паниковать
}

func TestSendDeliversAndDropsDeadSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := newService(t, true)

	var gotAuth, gotTTL string
	var gotBody int
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotTTL = r.Header.Get("Authorization"), r.Header.Get("TTL")
		b, _ := io.ReadAll(r.Body)
		gotBody = len(b)
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := s.Subscribe(ctx, "admin", browserSubscription(t, srv.URL+"/push"), ""); err != nil {
		t.Fatal(err)
	}
	if sent := s.Send(ctx, "admin", Payload{Title: "t", Body: "b", URL: "/support/1", Tag: "x"}); sent != 1 {
		t.Fatalf("sent = %d", sent)
	}
	if !strings.HasPrefix(gotAuth, "vapid ") || gotTTL == "" || gotBody == 0 {
		t.Fatalf("request must be VAPID-signed and encrypted: auth=%q ttl=%q body=%d", gotAuth, gotTTL, gotBody)
	}

	status = http.StatusGone
	if sent := s.Send(ctx, "admin", Payload{Title: "t"}); sent != 0 {
		t.Fatalf("sent = %d", sent)
	}
	if n, _ := s.Count(ctx, "admin"); n != 0 {
		t.Fatal("subscription that the push service reports as gone must be deleted")
	}
}

func TestNotifyStaffSendsToAdminOnly(t *testing.T) {
	ctx := context.Background()
	s := newService(t, true)
	hits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	if err := s.Subscribe(ctx, "admin", browserSubscription(t, srv.URL+"/admin-device"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "other", browserSubscription(t, srv.URL+"/other-device"), ""); err != nil {
		t.Fatal(err)
	}
	s.NotifyStaff("Новая заявка", "User: тема", "/support/1")

	select {
	case path := <-hits:
		if path != "/admin-device" {
			t.Fatalf("notification went to %s", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admin was not notified")
	}
	select {
	case path := <-hits:
		t.Fatalf("unexpected extra notification to %s", path)
	case <-time.After(300 * time.Millisecond):
	}
}

// ---- HTTP

func newApp(t *testing.T, s *Service, userID string) (*fiber.App, *webguard.Guard) {
	t.Helper()
	guard := webguard.New(webguard.Config{Secret: "secret", CookieName: "sid", Origins: []string{"https://site.example"}})
	h := NewHTTP(s, guard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app := fiber.New()
	api := app.Group("/api", func(c fiber.Ctx) error { return c.Next() })
	h.Register(api, func(fiber.Ctx) twitch.Identity { return twitch.Identity{UserID: userID} })
	return app, guard
}

func call(t *testing.T, app *fiber.App, g *webguard.Guard, method, path string, body any) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://site.example")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "session-1"})
	req.Header.Set(webguard.HeaderName, g.Token("session-1"))
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHTTPIsAdminOnly(t *testing.T) {
	s := newService(t, true)
	app, g := newApp(t, s, "someone")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/push/status"},
		{"POST", "/api/push/subscribe"},
		{"POST", "/api/push/unsubscribe"},
		{"POST", "/api/push/test"},
	} {
		if r := call(t, app, g, c.method, c.path, map[string]string{}); r.StatusCode != 403 {
			t.Errorf("%s %s for non-admin: %d", c.method, c.path, r.StatusCode)
		}
	}
}

func TestHTTPSubscribeFlow(t *testing.T) {
	s := newService(t, true)
	app, g := newApp(t, s, "admin")

	r := call(t, app, g, "GET", "/api/push/status", nil)
	var st struct {
		Enabled   bool   `json:"enabled"`
		PublicKey string `json:"publicKey"`
		Devices   int    `json:"devices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil || !st.Enabled || st.PublicKey == "" || st.Devices != 0 {
		t.Fatalf("status: %+v %v", st, err)
	}

	in := browserSubscription(t, "https://push.example/dev1")
	if r := call(t, app, g, "POST", "/api/push/subscribe", in); r.StatusCode != 204 {
		t.Fatalf("subscribe: %d", r.StatusCode)
	}
	if r := call(t, app, g, "POST", "/api/push/subscribe", map[string]any{"endpoint": "http://x"}); r.StatusCode != 422 {
		t.Fatalf("bad subscription: %d", r.StatusCode)
	}
	r = call(t, app, g, "GET", "/api/push/status", nil)
	_ = json.NewDecoder(r.Body).Decode(&st)
	if st.Devices != 1 {
		t.Fatalf("devices = %d", st.Devices)
	}
	if r := call(t, app, g, "POST", "/api/push/unsubscribe", map[string]string{"endpoint": in.Endpoint}); r.StatusCode != 204 {
		t.Fatalf("unsubscribe: %d", r.StatusCode)
	}
}
