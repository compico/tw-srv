package upload

import (
	"errors"
	"io"
	"log/slog"
	"time"

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
//	GET    /api/csrf              токен для заголовка X-CSRF-Token
//	POST   /api/uploads/images    multipart, поле file; ответ {id, url, name, ...}
//	GET    /api/uploads           мои загрузки
//	GET    /api/uploads/:id       файл
//	DELETE /api/uploads/:id       удалить (владелец или администратор)
func (h *HTTP) Register(api fiber.Router, identityOf func(fiber.Ctx) twitch.Identity) {
	h.identityOf = identityOf
	api.Get("/csrf", h.guard.TokenHandler)
	api.Options("/uploads/*", h.guard.Preflight)
	api.Post("/uploads/images", h.guard.Protect, h.upload)
	api.Get("/uploads", h.list)
	api.Get("/uploads/:id", h.guard.Protect, h.get)
	api.Delete("/uploads/:id", h.guard.Protect, h.remove)
}

type imageDTO struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toDTO(img *Image) imageDTO {
	return imageDTO{
		ID: img.ID, URL: "/api/uploads/" + img.ID, Name: img.Name,
		ContentType: img.ContentType, Size: img.Size, CreatedAt: img.CreatedAt,
	}
}

func (h *HTTP) upload(c fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Файл не передан (поле file)"})
	}
	if fh.Size > MaxSize {
		return h.fail(c, ErrTooLarge)
	}
	f, err := fh.Open()
	if err != nil {
		return h.fail(c, err)
	}
	defer f.Close()
	// Размер из заголовка multipart не доверяем: читаем с запасом в один байт.
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return h.fail(c, err)
	}
	img, err := h.svc.Save(c.Context(), h.identityOf(c).UserID, fh.Filename, data)
	if err != nil {
		return h.fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(toDTO(img))
}

func (h *HTTP) list(c fiber.Ctx) error {
	items, err := h.svc.List(c.Context(), h.identityOf(c).UserID)
	if err != nil {
		return h.fail(c, err)
	}
	out := make([]imageDTO, 0, len(items))
	for i := range items {
		out = append(out, toDTO(&items[i]))
	}
	return c.JSON(out)
}

func (h *HTTP) get(c fiber.Ctx) error {
	img, data, err := h.svc.Open(c.Context(), c.Params("id"))
	if err != nil {
		return h.fail(c, err)
	}
	c.Set("Content-Type", img.ContentType)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Set("Content-Disposition", "inline")
	// Содержимое по id не меняется; ссылка доступна только вошедшим, поэтому кэш только приватный.
	c.Set("Cache-Control", "private, max-age=31536000, immutable")
	return c.Send(data)
}

func (h *HTTP) remove(c fiber.Ctx) error {
	if err := h.svc.Delete(c.Context(), h.identityOf(c).UserID, c.Params("id")); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) fail(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": ErrNotFound.Error()})
	case errors.Is(err, ErrTooLarge):
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": ErrTooLarge.Error(), "code": "too_large"})
	case errors.Is(err, ErrUnsupported):
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": ErrUnsupported.Error(), "code": "unsupported"})
	case errors.Is(err, ErrEmpty):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ErrEmpty.Error()})
	case errors.Is(err, ErrQuota):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ErrQuota.Error(), "code": "quota"})
	}
	h.log.Error("upload request failed", "method", c.Method(), "path", c.Path(), "err", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "внутренняя ошибка"})
}
