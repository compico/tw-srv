package slowreq

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func run(t *testing.T, handlerDelay, threshold time.Duration) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	app := fiber.New()
	app.Use(New(log, threshold))
	app.Get("/work", func(c fiber.Ctx) error {
		time.Sleep(handlerDelay)
		return c.SendString("ok")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/work?code=secret", nil), fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return buf.String()
}

func TestLogsSlowRequest(t *testing.T) {
	out := run(t, 60*time.Millisecond, 20*time.Millisecond)
	if !strings.Contains(out, "slow request") || !strings.Contains(out, "path=/work") || !strings.Contains(out, "status=200") {
		t.Fatalf("slow request must be logged with method, path and status, got: %q", out)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "code=") {
		t.Fatalf("query string must not be logged: %q", out)
	}
}

func TestIgnoresFastRequest(t *testing.T) {
	if out := run(t, 0, time.Second); out != "" {
		t.Fatalf("fast request must not be logged, got: %q", out)
	}
}
