package push

import (
	"encoding/json"
	"errors"
	"log/slog"

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

// Register добавляет маршруты в группу /api (сессия уже проверена). Все они только для администратора:
//
//	GET  /api/push/status       {enabled, publicKey, devices}
//	POST /api/push/subscribe    тело: PushSubscription.toJSON()
//	POST /api/push/unsubscribe  тело: {endpoint}
//	POST /api/push/test         тестовое уведомление на устройства администратора
func (h *HTTP) Register(api fiber.Router, identityOf func(fiber.Ctx) twitch.Identity) {
	h.identityOf = identityOf
	api.Options("/push/*", h.guard.Preflight)
	api.Get("/push/status", h.admin, h.status)
	api.Post("/push/subscribe", h.guard.Protect, h.admin, h.subscribe)
	api.Post("/push/unsubscribe", h.guard.Protect, h.admin, h.unsubscribe)
	api.Post("/push/test", h.guard.Protect, h.admin, h.test)
}

func (h *HTTP) admin(c fiber.Ctx) error {
	if !h.svc.IsAdmin(h.identityOf(c).UserID) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Недоступно"})
	}
	return c.Next()
}

func (h *HTTP) status(c fiber.Ctx) error {
	n, err := h.svc.Count(c.Context(), h.identityOf(c).UserID)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(fiber.Map{"enabled": h.svc.Enabled(), "publicKey": h.svc.PublicKey(), "devices": n})
}

func (h *HTTP) subscribe(c fiber.Ctx) error {
	var in SubscriptionInput
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "некорректный JSON"})
	}
	if err := h.svc.Subscribe(c.Context(), h.identityOf(c).UserID, in, c.Get("User-Agent")); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) unsubscribe(c fiber.Ctx) error {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(c.Body(), &in); err != nil || in.Endpoint == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "некорректный JSON"})
	}
	if err := h.svc.Unsubscribe(c.Context(), h.identityOf(c).UserID, in.Endpoint); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) test(c fiber.Ctx) error {
	if !h.svc.Enabled() {
		return h.fail(c, ErrDisabled)
	}
	sent := h.svc.Send(c.Context(), h.identityOf(c).UserID, Payload{
		Title: "Тестовое уведомление", Body: "Уведомления о заявках работают", URL: "/support", Tag: "test",
	})
	return c.JSON(fiber.Map{"sent": sent})
}

func (h *HTTP) fail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, ErrDisabled):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": ErrDisabled.Error(), "code": "push_disabled"})
	case errors.Is(err, ErrBadSubscription):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ErrBadSubscription.Error()})
	case errors.Is(err, ErrTooMany):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ErrTooMany.Error()})
	}
	h.log.Error("push request failed", "method", c.Method(), "path", c.Path(), "err", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "внутренняя ошибка"})
}
