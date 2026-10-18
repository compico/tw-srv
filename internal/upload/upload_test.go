package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/platform/sqlitedb"
	"github.com/compico/tw-srv/internal/platform/webguard"
	"github.com/compico/tw-srv/internal/twitch"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n")

func pngBytes(n int) []byte { return append(append([]byte{}, pngHeader...), make([]byte, n)...) }

func newService(t *testing.T, adminID string) *Service {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(repo, store, adminID)
}

func TestSaveStoresOwnerAndContent(t *testing.T) {
	ctx := context.Background()
	s := newService(t, "")
	data := pngBytes(64)
	img, err := s.Save(ctx, "u1", "../../shot\x00.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if img.OwnerID != "u1" || img.ContentType != "image/png" || img.Size != int64(len(data)) || !validID(img.ID) {
		t.Fatalf("unexpected image: %+v", img)
	}
	if img.Name != "shot.png" {
		t.Errorf("file name must be sanitized, got %q", img.Name)
	}
	got, body, err := s.Open(ctx, img.ID)
	if err != nil || !bytes.Equal(body, data) || got.OwnerID != "u1" {
		t.Fatalf("open: %v %+v", err, got)
	}
	list, err := s.List(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list own: %v %d", err, len(list))
	}
	if other, _ := s.List(ctx, "u2"); len(other) != 0 {
		t.Fatal("other user must not see foreign uploads")
	}
}

func TestSaveRejectsBadFiles(t *testing.T) {
	ctx := context.Background()
	s := newService(t, "")
	cases := map[string]struct {
		data []byte
		want error
	}{
		"empty":   {nil, ErrEmpty},
		"too big": {pngBytes(MaxSize), ErrTooLarge},
		"text":    {[]byte("just text, not an image"), ErrUnsupported},
		"svg":     {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ErrUnsupported},
		"html":    {[]byte("<html><script>alert(1)</script></html>"), ErrUnsupported},
	}
	for name, c := range cases {
		if _, err := s.Save(ctx, "u1", "f.png", c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	// Ровно 5 МБ допустимо.
	if _, err := s.Save(ctx, "u1", "f.png", pngBytes(MaxSize-len(pngHeader))); err != nil {
		t.Errorf("file of exactly MaxSize must be accepted: %v", err)
	}
}

func TestQuota(t *testing.T) {
	ctx := context.Background()
	s := newService(t, "")
	for i := 0; i < MaxFilesPerOwner; i++ {
		if _, err := s.Save(ctx, "u1", "f.png", pngBytes(8)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Save(ctx, "u1", "f.png", pngBytes(8)); !errors.Is(err, ErrQuota) {
		t.Fatalf("files quota: %v", err)
	}
	if _, err := s.Save(ctx, "u2", "f.png", pngBytes(8)); err != nil {
		t.Fatalf("quota is per user: %v", err)
	}
}

func TestDeletePermissions(t *testing.T) {
	ctx := context.Background()
	s := newService(t, "admin")
	img, err := s.Save(ctx, "u1", "f.png", pngBytes(8))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "u2", img.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger must not delete: %v", err)
	}
	if err := s.Delete(ctx, "admin", img.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, _, err := s.Open(ctx, img.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted file must be gone: %v", err)
	}
	if _, _, err := s.Open(ctx, "../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid id: %v", err)
	}
}

// ---- HTTP: загрузка через multipart с CSRF-токеном

func newApp(t *testing.T) (*fiber.App, *webguard.Guard) {
	t.Helper()
	s := newService(t, "")
	guard := webguard.New(webguard.Config{Secret: "secret", CookieName: "sid", Origins: []string{"https://site.example"}})
	h := NewHTTP(s, guard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	app := fiber.New(fiber.Config{BodyLimit: MaxSize + 1<<20})
	api := app.Group("/api", func(c fiber.Ctx) error { return c.Next() })
	h.Register(api, func(fiber.Ctx) twitch.Identity { return twitch.Identity{UserID: "u1"} })
	return app, guard
}

func multipartBody(t *testing.T, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.Close()
	return &buf, w.FormDataContentType()
}

func post(t *testing.T, app *fiber.App, g *webguard.Guard, token string, data []byte) *http.Response {
	t.Helper()
	body, ct := multipartBody(t, "shot.png", data)
	req := httptest.NewRequest("POST", "/api/uploads/images", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Origin", "https://site.example")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "session-1"})
	if token != "" {
		req.Header.Set(webguard.HeaderName, token)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestUploadEndpoint(t *testing.T) {
	app, g := newApp(t)
	token := g.Token("session-1")

	if r := post(t, app, g, "", pngBytes(32)); r.StatusCode != 403 {
		t.Fatalf("upload without CSRF token: %d", r.StatusCode)
	}
	if r := post(t, app, g, token, []byte("not an image")); r.StatusCode != 415 {
		t.Fatalf("non-image: %d", r.StatusCode)
	}
	if r := post(t, app, g, token, pngBytes(MaxSize)); r.StatusCode != 413 {
		t.Fatalf("too large: %d", r.StatusCode)
	}

	r := post(t, app, g, token, pngBytes(32))
	if r.StatusCode != 201 {
		t.Fatalf("upload: %d", r.StatusCode)
	}
	var dto imageDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dto.URL, "/api/uploads/") || dto.ContentType != "image/png" || dto.Name != "shot.png" {
		t.Fatalf("unexpected response: %+v", dto)
	}

	req := httptest.NewRequest("GET", dto.URL, nil)
	got, err := app.Test(req)
	if err != nil || got.StatusCode != 200 {
		t.Fatalf("download: %v %v", err, got)
	}
	if got.Header.Get("Content-Type") != "image/png" || got.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("unsafe download headers: %v", got.Header)
	}
}
