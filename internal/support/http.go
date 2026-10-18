package support

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/twitch"
)

const maxRequestBody = 64 * 1024

type HTTP struct {
	svc        *Service
	log        *slog.Logger
	identityOf func(fiber.Ctx) twitch.Identity
}

func NewHTTP(svc *Service, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, log: log}
}

// Register добавляет маршруты в группу /api, уже защищённую проверкой сессии.
func (h *HTTP) Register(api fiber.Router, identityOf func(fiber.Ctx) twitch.Identity) {
	h.identityOf = identityOf
	api.Get("/support/summary", h.summary)
	api.Get("/support/tickets", h.list)
	api.Post("/support/tickets", h.create)
	api.Get("/support/tickets/:id", h.get)
	api.Post("/support/tickets/:id/messages", h.reply)
	api.Post("/support/tickets/:id/close", h.close)
}

type ticketDTO struct {
	ID         uint      `json:"id"`
	Subject    string    `json:"subject"`
	AuthorName string    `json:"authorName"`
	Mine       bool      `json:"mine"`
	Status     string    `json:"status"` // open | waiting | closed, с точки зрения смотрящего
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type messageDTO struct {
	ID         uint      `json:"id"`
	AuthorName string    `json:"authorName"`
	Mine       bool      `json:"mine"`
	Staff      bool      `json:"staff"`
	Format     string    `json:"format"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

func toTicketDTO(t Ticket, viewerID string) ticketDTO {
	return ticketDTO{
		ID: t.ID, Subject: t.Subject, AuthorName: t.AuthorName, Mine: t.AuthorID == viewerID,
		Status: t.ViewStatus(viewerID), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func toTicketDTOs(ts []Ticket, viewerID string) []ticketDTO {
	out := make([]ticketDTO, 0, len(ts))
	for _, t := range ts {
		out = append(out, toTicketDTO(t, viewerID))
	}
	return out
}

func (h *HTTP) viewer(c fiber.Ctx) Viewer {
	id := h.identityOf(c)
	name := id.DisplayName
	if name == "" {
		name = id.Login
	}
	return Viewer{ID: id.UserID, Name: name}
}

func (h *HTTP) summary(c fiber.Ctx) error {
	awaiting, isAdmin, err := h.svc.Summary(c.Context(), h.viewer(c))
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(fiber.Map{"awaiting": awaiting, "isAdmin": isAdmin})
}

// list: ?scope=all (только администратор) или свои заявки.
func (h *HTTP) list(c fiber.Ctx) error {
	v := h.viewer(c)
	var (
		ts  []Ticket
		err error
	)
	if c.Query("scope") == "all" {
		ts, err = h.svc.ListAll(c.Context(), v)
	} else {
		ts, err = h.svc.ListMine(c.Context(), v)
	}
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(toTicketDTOs(ts, v.ID))
}

type createInput struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type replyInput struct {
	Body string `json:"body"`
}

func (h *HTTP) create(c fiber.Ctx) error {
	var in createInput
	if !h.decode(c, &in) {
		return nil
	}
	v := h.viewer(c)
	t, err := h.svc.CreateTicket(c.Context(), v, in.Subject, in.Body)
	if err != nil {
		return h.fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(toTicketDTO(*t, v.ID))
}

func (h *HTTP) get(c fiber.Ctx) error {
	id, ok := ticketID(c)
	if !ok {
		return h.fail(c, ErrNotFound)
	}
	v := h.viewer(c)
	t, msgs, err := h.svc.Get(c.Context(), v, id)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(h.detail(t, msgs, v))
}

func (h *HTTP) reply(c fiber.Ctx) error {
	id, ok := ticketID(c)
	if !ok {
		return h.fail(c, ErrNotFound)
	}
	var in replyInput
	if !h.decode(c, &in) {
		return nil
	}
	v := h.viewer(c)
	if _, err := h.svc.Reply(c.Context(), v, id, in.Body); err != nil {
		return h.fail(c, err)
	}
	t, msgs, err := h.svc.Get(c.Context(), v, id)
	if err != nil {
		return h.fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(h.detail(t, msgs, v))
}

func (h *HTTP) close(c fiber.Ctx) error {
	id, ok := ticketID(c)
	if !ok {
		return h.fail(c, ErrNotFound)
	}
	v := h.viewer(c)
	if _, err := h.svc.Close(c.Context(), v, id); err != nil {
		return h.fail(c, err)
	}
	t, msgs, err := h.svc.Get(c.Context(), v, id)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(h.detail(t, msgs, v))
}

func (h *HTTP) detail(t *Ticket, msgs []Message, v Viewer) fiber.Map {
	out := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageDTO{
			ID: m.ID, AuthorName: m.AuthorName, Mine: m.AuthorID == v.ID, Staff: m.Staff,
			Format: m.Format, Body: m.Body, CreatedAt: m.CreatedAt,
		})
	}
	return fiber.Map{"ticket": toTicketDTO(*t, v.ID), "messages": out}
}

func ticketID(c fiber.Ctx) (uint, bool) {
	n, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}

// decode разбирает JSON-тело; при ошибке сам отвечает клиенту и возвращает false.
func (h *HTTP) decode(c fiber.Ctx, out any) bool {
	body := c.Body()
	if len(body) > maxRequestBody {
		_ = c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "Слишком большой запрос"})
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		_ = c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "некорректный JSON"})
		return false
	}
	return true
}

func (h *HTTP) fail(c fiber.Ctx, err error) error {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ve.Message, "field": ve.Field})
	case errors.Is(err, ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Заявка не найдена"})
	case errors.Is(err, ErrClosed):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Заявка закрыта", "code": "ticket_closed"})
	}
	h.log.Error("support request failed", "method", c.Method(), "path", c.Path(), "err", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "внутренняя ошибка"})
}
