package auth

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

type HTTPConfig struct {
	CookieName   string
	CookieSecure bool
	SessionTTL   time.Duration
}

// HTTP: браузерные эндпоинты входа через Twitch (всё под /auth).
type HTTP struct {
	svc *Service
	cfg HTTPConfig
	log *slog.Logger
}

func NewHTTP(svc *Service, cfg HTTPConfig, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, cfg: cfg, log: log}
}

const stateCookie = "oc_login_state"

func (h *HTTP) Register(app *fiber.App) {
	app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })

	g := app.Group("/auth")
	g.Get("/twitch/login", h.login)
	g.Get("/twitch/callback", h.callback)
	g.Post("/logout", h.logout)
}

func (h *HTTP) login(c fiber.Ctx) error {
	authURL, state, err := h.svc.BeginLogin(c.Context(), c.Query("return_to"))
	if err != nil {
		h.log.Error("begin login", "err", err)
		return c.Status(fiber.StatusInternalServerError).SendString("не удалось начать вход")
	}
	c.Cookie(&fiber.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/auth",
		MaxAge:   600,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
	})
	c.Set("Location", authURL)
	return c.SendStatus(fiber.StatusFound)
}

func (h *HTTP) callback(c fiber.Ctx) error {
	if e := c.Query("error"); e != "" {
		// Пользователь отказал в доступе на странице Twitch.
		c.Set("Location", "/?login_error="+e)
		return c.SendStatus(fiber.StatusFound)
	}
	state, code := c.Query("state"), c.Query("code")
	if state == "" || code == "" || state != c.Cookies(stateCookie) {
		return c.Status(fiber.StatusBadRequest).SendString("некорректный запрос входа, попробуйте ещё раз")
	}

	sessionID, returnTo, err := h.svc.CompleteLogin(c.Context(), code, state)
	if err != nil {
		h.log.Warn("complete login", "err", err)
		if errors.Is(err, ErrBadState) || errors.Is(err, ErrMissingScope) {
			return c.Status(fiber.StatusBadRequest).SendString("вход не удался: " + err.Error())
		}
		return c.Status(fiber.StatusBadGateway).SendString("вход не удался, попробуйте позже")
	}

	c.Cookie(&fiber.Cookie{Name: stateCookie, Value: "", Path: "/auth", MaxAge: -1, HTTPOnly: true, Secure: h.cfg.CookieSecure, SameSite: "Lax"})
	c.Cookie(&fiber.Cookie{
		Name:     h.cfg.CookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   int(h.cfg.SessionTTL.Seconds()),
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
	})
	c.Set("Location", returnTo)
	return c.SendStatus(fiber.StatusFound)
}

func (h *HTTP) logout(c fiber.Ctx) error {
	if err := h.svc.Logout(c.Context(), c.Cookies(h.cfg.CookieName)); err != nil {
		h.log.Warn("logout", "err", err)
	}
	c.Cookie(&fiber.Cookie{Name: h.cfg.CookieName, Value: "", Path: "/", MaxAge: -1, HTTPOnly: true, Secure: h.cfg.CookieSecure, SameSite: "Lax"})
	return c.SendStatus(fiber.StatusNoContent)
}
