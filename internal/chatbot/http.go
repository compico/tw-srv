package chatbot

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/platform/webguard"
	"github.com/compico/tw-srv/internal/twitch"
)

type HTTP struct {
	svc        *Service
	guard      *webguard.Guard
	log        *slog.Logger
	identityOf func(fiber.Ctx) twitch.Identity
}

func NewHTTP(svc *Service, guard *webguard.Guard, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, guard: guard, log: log}
}

// Register добавляет маршруты в группу /api (сессия уже проверена):
//
//	GET    /api/chat            состояние: бот и шаблоны
//	POST   /api/chat/templates  сохранить шаблоны {success:{enabled,text}, fail:{enabled,text}}
//	GET    /api/bot/connect     начало подключения бота (редирект на Twitch)
//	POST   /api/bot/link        ссылка авторизации для другого браузера {url}
//	GET    /api/bot/callback    возврат с Twitch (публичный, см. RegisterPublic)
//	DELETE /api/bot             отключить бота
func (h *HTTP) Register(api fiber.Router, identityOf func(fiber.Ctx) twitch.Identity) {
	h.identityOf = identityOf
	api.Options("/chat/*", h.guard.Preflight)
	api.Options("/bot", h.guard.Preflight)
	api.Get("/chat", h.state)
	api.Post("/chat/templates", h.guard.Protect, h.saveTemplates)
	api.Get("/bot/connect", h.connect)
	api.Post("/bot/link", h.guard.Protect, h.link)
	api.Delete("/bot", h.guard.Protect, h.disconnect)
}

// RegisterPublic регистрирует маршруты без проверки сессии (возврат с Twitch).
func (h *HTTP) RegisterPublic(app *fiber.App) {
	app.Get("/api/bot/callback", h.callback)
}

type botDTO struct {
	Connected   bool   `json:"connected"`
	Login       string `json:"login,omitempty"`
	NeedsReauth bool   `json:"needsReauth"`
	LastError   string `json:"lastError,omitempty"`
}

func (h *HTTP) state(c fiber.Ctx) error {
	id := h.identityOf(c).UserID
	tpl, err := h.svc.Templates(c.Context(), id)
	if err != nil {
		return h.fail(c, err)
	}
	bot := botDTO{}
	if b, err := h.svc.Bot(c.Context(), id); err != nil {
		return h.fail(c, err)
	} else if b != nil {
		bot = botDTO{Connected: !b.Invalid, Login: b.BotLogin, NeedsReauth: b.Invalid, LastError: b.LastError}
	}
	return c.JSON(fiber.Map{
		"available": h.svc.Enabled(),
		"bot":       bot,
		"templates": tpl,
		"variables": Variables,
	})
}

func (h *HTTP) saveTemplates(c fiber.Ctx) error {
	var in Templates
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "некорректный JSON"})
	}
	out, err := h.svc.SaveTemplates(c.Context(), h.identityOf(c).UserID, in)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(out)
}

// connect: подключение в этом же браузере (редирект на Twitch).
func (h *HTTP) connect(c fiber.Ctx) error {
	authURL, err := h.begin(c)
	if err != nil {
		return h.fail(c, err)
	}
	c.Set("Location", authURL)
	return c.SendStatus(fiber.StatusFound)
}

// link: ссылка авторизации для другого браузера (например, приватного окна с аккаунтом бота).
func (h *HTTP) link(c fiber.Ctx) error {
	authURL, err := h.begin(c)
	if err != nil {
		return h.fail(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"url": authURL, "expiresInSeconds": int(ConnectTTL.Seconds())})
}

func (h *HTTP) begin(c fiber.Ctx) (string, error) {
	return h.svc.BeginConnect(c.Context(), h.identityOf(c).UserID)
}

// callback открыт без сессии: возврат с Twitch может прийти в другой браузер. Доверие даёт одноразовое
// состояние, созданное вошедшим стримером. Результат показывает публичная страница /bot/result.
func (h *HTTP) callback(c fiber.Ctx) error {
	back := func(val string) error {
		c.Set("Cache-Control", "no-store")
		c.Set("Location", "/bot/result?status="+url.QueryEscape(val))
		return c.SendStatus(fiber.StatusFound)
	}
	if e := c.Query("error"); e != "" {
		return back("denied")
	}
	state, code := c.Query("state"), c.Query("code")
	if state == "" || code == "" {
		return back("bad_state")
	}
	if err := h.svc.CompleteConnect(c.Context(), state, code); err != nil {
		h.log.Warn("chatbot: complete connect", "err", err)
		switch {
		case errors.Is(err, ErrBadState):
			return back("bad_state")
		case errors.Is(err, ErrBadScope):
			return back("bad_scope")
		}
		return back("failed")
	}
	return back("connected")
}

func (h *HTTP) disconnect(c fiber.Ctx) error {
	if err := h.svc.Disconnect(c.Context(), h.identityOf(c).UserID); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) fail(c fiber.Ctx, err error) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ve.Message, "field": ve.Field})
	}
	h.log.Error("chatbot: request failed", "method", c.Method(), "path", c.Path(), "err", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "внутренняя ошибка"})
}
