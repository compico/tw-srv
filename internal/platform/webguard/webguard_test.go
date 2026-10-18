package webguard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newGuard() *Guard {
	return New(Config{Secret: "secret", CookieName: "sid", Origins: []string{"https://site.example/", "http://localhost:8080"}})
}

func TestTokenIsBoundToSession(t *testing.T) {
	g := newGuard()
	if !g.valid("session-1", g.Token("session-1")) {
		t.Fatal("own token must be valid")
	}
	if g.valid("session-2", g.Token("session-1")) {
		t.Fatal("token of another session must be rejected")
	}
	if g.valid("", g.Token("")) || g.valid("session-1", "") {
		t.Fatal("empty session or token must be rejected")
	}
	other := New(Config{Secret: "other", CookieName: "sid"})
	if g.valid("session-1", other.Token("session-1")) {
		t.Fatal("token signed with another secret must be rejected")
	}
}

func TestOriginAllowed(t *testing.T) {
	g := newGuard()
	for origin, want := range map[string]bool{
		"https://site.example":      true,
		"https://SITE.example":      true,
		"http://localhost:8080":     true,
		"http://site.example":       false,
		"https://evil.example":      false,
		"https://site.example.evil": false,
		"null":                      false,
		"":                          false,
	} {
		if got := g.OriginAllowed(origin); got != want {
			t.Errorf("OriginAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
}

func newApp(g *Guard) *fiber.App {
	app := fiber.New()
	app.Options("/x", g.Preflight)
	app.Post("/x", g.Protect, func(c fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/x", g.Protect, func(c fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func do(t *testing.T, app *fiber.App, method, origin, token string, withCookie bool) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set(HeaderName, token)
	}
	if withCookie {
		req.AddCookie(&http.Cookie{Name: "sid", Value: "session-1"})
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestProtect(t *testing.T) {
	g := newGuard()
	app := newApp(g)
	good := g.Token("session-1")

	if r := do(t, app, "POST", "https://site.example", good, true); r.StatusCode != 200 {
		t.Errorf("valid request: %d", r.StatusCode)
	}
	if r := do(t, app, "POST", "https://site.example", "", true); r.StatusCode != 403 {
		t.Errorf("missing token: %d", r.StatusCode)
	}
	if r := do(t, app, "POST", "https://site.example", "bad", true); r.StatusCode != 403 {
		t.Errorf("wrong token: %d", r.StatusCode)
	}
	if r := do(t, app, "POST", "https://site.example", good, false); r.StatusCode != 403 {
		t.Errorf("no session cookie: %d", r.StatusCode)
	}
	if r := do(t, app, "POST", "https://evil.example", good, true); r.StatusCode != 403 {
		t.Errorf("foreign origin: %d", r.StatusCode)
	}
	// GET не меняет состояние: токен не нужен, но чужой Origin по-прежнему отклоняется.
	if r := do(t, app, "GET", "", "", true); r.StatusCode != 200 {
		t.Errorf("safe method: %d", r.StatusCode)
	}
	if r := do(t, app, "GET", "https://evil.example", "", true); r.StatusCode != 403 {
		t.Errorf("safe method, foreign origin: %d", r.StatusCode)
	}
}

func TestPreflight(t *testing.T) {
	g := newGuard()
	app := newApp(g)

	r := do(t, app, "OPTIONS", "http://localhost:8080", "", false)
	if r.StatusCode != 204 {
		t.Fatalf("allowed origin preflight: %d", r.StatusCode)
	}
	if r.Header.Get("Access-Control-Allow-Origin") != "http://localhost:8080" || r.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("missing CORS headers: %v", r.Header)
	}
	if r.Header.Get("Access-Control-Allow-Headers") == "" {
		t.Error("preflight must list allowed headers")
	}
	if r := do(t, app, "OPTIONS", "https://evil.example", "", false); r.StatusCode != 403 {
		t.Errorf("foreign origin preflight: %d", r.StatusCode)
	}
}
