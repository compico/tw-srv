// Package webguard защищает изменяющие запросы к API, которые делает только наш сайт:
// проверяет заголовок X-CSRF-Token и Origin и отвечает на CORS-preflight.
//
// Токен привязан к сессии: HMAC(ключ, id сессии), поэтому состояния на сервере нет, а токен
// чужой сессии не подходит. Его отдаёт GET /api/csrf, фронтенд кладёт его в заголовок.
package webguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const HeaderName = "X-CSRF-Token"

type Config struct {
	Secret     string   // секрет приложения: из него выводится ключ CSRF (отдельной переменной не нужно)
	CookieName string   // cookie сессии
	Origins    []string // источники, которым разрешены запросы с cookie (CORS); пути и слэш в конце игнорируются
}

type Guard struct {
	key     []byte
	cookie  string
	origins map[string]bool
}

func New(cfg Config) *Guard {
	kdf := hmac.New(sha256.New, []byte(cfg.Secret))
	kdf.Write([]byte("tw-srv csrf v1"))
	g := &Guard{key: kdf.Sum(nil), cookie: cfg.CookieName, origins: map[string]bool{}}
	for _, o := range cfg.Origins {
		if n := NormalizeOrigin(o); n != "" {
			g.origins[n] = true
		}
	}
	return g
}

// NormalizeOrigin приводит URL к виду scheme://host[:port] в нижнем регистре; пустая строка, если это не URL.
func NormalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func (g *Guard) OriginAllowed(origin string) bool {
	return g.origins[NormalizeOrigin(origin)]
}

// Token возвращает CSRF-токен для сессии.
func (g *Guard) Token(sessionID string) string {
	mac := hmac.New(sha256.New, g.key)
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (g *Guard) valid(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(g.Token(sessionID)), []byte(token))
}

func safeMethod(m string) bool {
	return m == fiber.MethodGet || m == fiber.MethodHead || m == fiber.MethodOptions
}

func deny(c fiber.Ctx, code, msg string) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": msg, "code": code})
}

func (g *Guard) cors(c fiber.Ctx, origin string) {
	c.Set("Access-Control-Allow-Origin", origin)
	c.Set("Access-Control-Allow-Credentials", "true")
	c.Set("Vary", "Origin")
}

// Protect проверяет Origin (если он передан) и для изменяющих методов CSRF-токен.
func (g *Guard) Protect(c fiber.Ctx) error {
	if origin := c.Get("Origin"); origin != "" {
		if !g.OriginAllowed(origin) {
			return deny(c, "origin_not_allowed", "Недопустимый источник запроса")
		}
		g.cors(c, origin)
	}
	if safeMethod(c.Method()) {
		return c.Next()
	}
	if !g.valid(c.Cookies(g.cookie), c.Get(HeaderName)) {
		return deny(c, "csrf", "Недействительный CSRF-токен: обновите страницу")
	}
	return c.Next()
}

// Preflight отвечает на CORS-preflight (OPTIONS) для разрешённых источников.
func (g *Guard) Preflight(c fiber.Ctx) error {
	origin := c.Get("Origin")
	if origin == "" {
		return c.SendStatus(fiber.StatusNoContent)
	}
	if !g.OriginAllowed(origin) {
		return deny(c, "origin_not_allowed", "Недопустимый источник запроса")
	}
	g.cors(c, origin)
	c.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	c.Set("Access-Control-Allow-Headers", "Content-Type, "+HeaderName)
	c.Set("Access-Control-Max-Age", "600")
	return c.SendStatus(fiber.StatusNoContent)
}

// TokenHandler отдаёт токен текущей сессии. Маршрут должен стоять за проверкой сессии.
func (g *Guard) TokenHandler(c fiber.Ctx) error {
	sid := c.Cookies(g.cookie)
	if sid == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"token": g.Token(sid), "header": HeaderName})
}
