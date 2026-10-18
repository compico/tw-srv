package memealert

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/compico/tw-srv/internal/platform/view"
	"github.com/compico/tw-srv/internal/twitch"
)

// RouteRegistrar добавляет маршруты других доменов (например, поддержки) в группу /api,
// где уже проверена сессия. identityOf достаёт из запроса вошедшего пользователя.
type RouteRegistrar func(api fiber.Router, identityOf func(fiber.Ctx) twitch.Identity)

type HTTPConfig struct {
	CookieName   string // имя cookie сессии (его выставляет сервис twitch-auth)
	CookieSecure bool
	View         *view.View       // HTML-оболочка и ассеты Vite
	Extra        []RouteRegistrar // маршруты других доменов под /api
}

type HTTP struct {
	svc      *Service
	sessions twitch.SessionValidator
	cfg      HTTPConfig
	log      *slog.Logger
}

func NewHTTP(svc *Service, sessions twitch.SessionValidator, cfg HTTPConfig, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, sessions: sessions, cfg: cfg, log: log}
}

const (
	identityKey = "identity"
	maStateCook = "oc_ma_state"
)

func (h *HTTP) Register(app *fiber.App) {
	app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })

	api := app.Group("/api", h.requireSession)
	api.Get("/me", h.me)
	api.Get("/memealerts/connect", h.connect)
	api.Get("/memealerts/callback", h.callback)
	api.Delete("/memealerts", h.disconnect)
	api.Get("/reward", h.getReward)
	api.Put("/reward", h.putReward)
	api.Delete("/reward", h.deleteReward)
	api.Get("/redemptions", h.redemptions)
	for _, register := range h.cfg.Extra {
		register(api, identityOf)
	}
	app.All("/api/*", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not found"})
	})

	app.Get("/assets/*", h.cfg.View.AssetsHandler)
	app.Get("/*", h.cfg.View.IndexHandler)
}

// ---------------------------------------------------------------- middleware

func (h *HTTP) requireSession(c fiber.Ctx) error {
	id, err := h.sessions.ValidateSession(c.Context(), c.Cookies(h.cfg.CookieName))
	if err != nil {
		if errors.Is(err, twitch.ErrNoSession) {
			// Навигация браузера (подключение MemeAlerts): ведём на вход, а не показываем JSON.
			if strings.Contains(c.Get("Accept"), "text/html") {
				c.Set("Location", "/auth/twitch/login?return_to=/")
				return c.SendStatus(fiber.StatusFound)
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized", "loginUrl": "/auth/twitch/login"})
		}
		h.log.Error("validate session", "err", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "сервис авторизации недоступен"})
	}
	c.Locals(identityKey, id)
	return c.Next()
}

func identityOf(c fiber.Ctx) twitch.Identity {
	id, _ := c.Locals(identityKey).(twitch.Identity)
	return id
}

// ---------------------------------------------------------------- DTO

type rewardDTO struct {
	RewardInput
	TwitchRewardID string    `json:"twitchRewardId"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func toRewardDTO(r *Reward) *rewardDTO {
	if r == nil {
		return nil
	}
	return &rewardDTO{
		RewardInput: RewardInput{
			Title:               r.Title,
			Prompt:              r.Prompt,
			Cost:                r.Cost,
			BonusAmount:         r.BonusAmount,
			UseUserInput:        r.UseUserInput,
			BackgroundColor:     r.BackgroundColor,
			Enabled:             r.Enabled,
			CooldownSeconds:     r.CooldownSeconds,
			MaxPerStream:        r.MaxPerStream,
			MaxPerUserPerStream: r.MaxPerUserPerStream,
		},
		TwitchRewardID: r.TwitchRewardID,
		UpdatedAt:      r.UpdatedAt,
	}
}

type redemptionDTO struct {
	ID          string    `json:"id"`
	ViewerName  string    `json:"viewerName"`
	UserInput   string    `json:"userInput"`
	MatchedNick string    `json:"matchedNick"`
	Amount      int       `json:"amount"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ---------------------------------------------------------------- handlers

func (h *HTTP) me(c fiber.Ctx) error {
	id := identityOf(c)
	ov, err := h.svc.Overview(c.Context(), id.UserID)
	if err != nil {
		return h.fail(c, err)
	}

	resp := fiber.Map{
		"twitch": fiber.Map{
			"id":          id.UserID,
			"login":       id.Login,
			"displayName": id.DisplayName,
			"avatarUrl":   id.AvatarURL,
		},
		"twitchReauth": false,
		"memealerts":   fiber.Map{"connected": false, "needsReauth": false},
		"reward":       toRewardDTO(ov.Reward),
	}
	if ov.Account != nil {
		resp["twitchReauth"] = ov.Account.TwitchReauth
		resp["memealerts"] = fiber.Map{
			"connected":   true,
			"needsReauth": ov.Account.Invalid,
			"name":        ov.Account.MemeAlertsName,
			"link":        ov.Account.MemeAlertsLink,
		}
	}
	return c.JSON(resp)
}

func (h *HTTP) connect(c fiber.Ctx) error {
	authURL, state, err := h.svc.BeginConnect(c.Context(), identityOf(c).UserID)
	if err != nil {
		return h.fail(c, err)
	}
	c.Cookie(&fiber.Cookie{
		Name:     maStateCook,
		Value:    state,
		Path:     "/api/memealerts",
		MaxAge:   600,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
	})
	c.Set("Location", authURL)
	return c.SendStatus(fiber.StatusFound)
}

func (h *HTTP) callback(c fiber.Ctx) error {
	back := func(key, val string) error {
		c.Cookie(&fiber.Cookie{Name: maStateCook, Value: "", Path: "/api/memealerts", MaxAge: -1, HTTPOnly: true, Secure: h.cfg.CookieSecure, SameSite: "Lax"})
		c.Set("Location", "/?"+key+"="+url.QueryEscape(val))
		return c.SendStatus(fiber.StatusFound)
	}

	if e := c.Query("error"); e != "" {
		return back("memealerts_error", e)
	}
	state, code := c.Query("state"), c.Query("code")
	if state == "" || code == "" || state != c.Cookies(maStateCook) {
		return back("memealerts_error", "bad_state")
	}
	if err := h.svc.CompleteConnect(c.Context(), identityOf(c), state, code); err != nil {
		h.log.Warn("complete memealerts connect", "err", err)
		if errors.Is(err, ErrBadState) {
			return back("memealerts_error", "bad_state")
		}
		return back("memealerts_error", "failed")
	}
	return back("memealerts", "connected")
}

func (h *HTTP) disconnect(c fiber.Ctx) error {
	if err := h.svc.Disconnect(c.Context(), identityOf(c).UserID); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) getReward(c fiber.Ctx) error {
	ov, err := h.svc.Overview(c.Context(), identityOf(c).UserID)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(toRewardDTO(ov.Reward))
}

func (h *HTTP) putReward(c fiber.Ctx) error {
	var in RewardInput
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "некорректный JSON"})
	}
	rw, err := h.svc.SaveReward(c.Context(), identityOf(c).UserID, in)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(toRewardDTO(rw))
}

func (h *HTTP) deleteReward(c fiber.Ctx) error {
	if err := h.svc.DeleteReward(c.Context(), identityOf(c).UserID); err != nil {
		return h.fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *HTTP) redemptions(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	items, err := h.svc.History(c.Context(), identityOf(c).UserID, limit)
	if err != nil {
		return h.fail(c, err)
	}
	out := make([]redemptionDTO, 0, len(items))
	for _, it := range items {
		out = append(out, redemptionDTO{
			ID:          it.ID,
			ViewerName:  it.ViewerName,
			UserInput:   it.UserInput,
			MatchedNick: it.MatchedNick,
			Amount:      it.Amount,
			Status:      string(it.Status),
			Reason:      it.Reason,
			CreatedAt:   it.CreatedAt,
		})
	}
	return c.JSON(out)
}

// fail переводит доменные ошибки в HTTP-ответы с кодами для фронтенда.
func (h *HTTP) fail(c fiber.Ctx, err error) error {
	var ve *ValidationError
	var ue *UserError
	switch {
	case errors.As(err, &ve):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": ve.Message, "field": ve.Field})
	case errors.As(err, &ue):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": ue.Message})
	case errors.Is(err, ErrNotConnected):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Сначала подключите MemeAlerts", "code": "memealerts_not_connected"})
	case errors.Is(err, ErrMemeAlertsReauth):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Доступ к MemeAlerts отозван: подключите аккаунт заново", "code": "memealerts_reauth"})
	case errors.Is(err, twitch.ErrReauthRequired):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Нужно заново войти через Twitch", "code": "twitch_reauth"})
	}
	h.log.Error("request failed", "method", c.Method(), "path", c.Path(), "err", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "внутренняя ошибка"})
}
