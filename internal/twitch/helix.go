package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/compico/overlaycomposer/internal/platform/httpx"
)

const helixBase = "https://api.twitch.tv/helix"

// Helix: тонкий клиент к нужным нам методам Twitch API.
type Helix struct {
	http *http.Client
	base string
}

func NewHelix() *Helix {
	return &Helix{http: &http.Client{Timeout: 15 * time.Second}, base: helixBase}
}

// Reward: награда за баллы канала (ответ Helix).
type Reward struct {
	ID                  string `json:"id"`
	BroadcasterID       string `json:"broadcaster_id"`
	Title               string `json:"title"`
	Prompt              string `json:"prompt"`
	Cost                int    `json:"cost"`
	IsEnabled           bool   `json:"is_enabled"`
	IsUserInputRequired bool   `json:"is_user_input_required"`
}

// RewardInput: тело создания и обновления награды.
// Булевы поля отправляются всегда, чтобы PATCH мог выключать ограничения.
// Числовые лимиты отправляются только когда включены (Twitch не принимает 0).
type RewardInput struct {
	Title                             string `json:"title"`
	Cost                              int    `json:"cost"`
	Prompt                            string `json:"prompt"`
	IsEnabled                         bool   `json:"is_enabled"`
	BackgroundColor                   string `json:"background_color,omitempty"`
	IsUserInputRequired               bool   `json:"is_user_input_required"`
	IsMaxPerStreamEnabled             bool   `json:"is_max_per_stream_enabled"`
	MaxPerStream                      int    `json:"max_per_stream,omitempty"`
	IsMaxPerUserPerStreamEnabled      bool   `json:"is_max_per_user_per_stream_enabled"`
	MaxPerUserPerStream               int    `json:"max_per_user_per_stream,omitempty"`
	IsGlobalCooldownEnabled           bool   `json:"is_global_cooldown_enabled"`
	GlobalCooldownSeconds             int    `json:"global_cooldown_seconds,omitempty"`
	ShouldRedemptionsSkipRequestQueue bool   `json:"should_redemptions_skip_request_queue"`
}

// Redemption: использование награды зрителем.
// Одна структура покрывает и EventSub-уведомление, и ответ Helix:
// у них различаются только имена полей стримера.
type Redemption struct {
	ID                string    `json:"id"`
	BroadcasterUserID string    `json:"broadcaster_user_id"`
	BroadcasterID     string    `json:"broadcaster_id"`
	UserID            string    `json:"user_id"`
	UserLogin         string    `json:"user_login"`
	UserName          string    `json:"user_name"`
	UserInput         string    `json:"user_input"`
	Status            string    `json:"status"`
	RedeemedAt        time.Time `json:"redeemed_at"`
	Reward            struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
		Cost   int    `json:"cost"`
	} `json:"reward"`
}

const (
	StatusFulfilled = "FULFILLED"
	StatusCanceled  = "CANCELED"
)

func (h *Helix) do(ctx context.Context, creds Credentials, method, path string, q url.Values, body, out any) error {
	u := h.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("Client-Id", creds.ClientID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return httpx.DoJSON(h.http, req, out)
}

func (h *Helix) CreateReward(ctx context.Context, c Credentials, broadcasterID string, in RewardInput) (*Reward, error) {
	var out struct {
		Data []Reward `json:"data"`
	}
	q := url.Values{"broadcaster_id": {broadcasterID}}
	if err := h.do(ctx, c, http.MethodPost, "/channel_points/custom_rewards", q, in, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return &out.Data[0], nil
}

func (h *Helix) UpdateReward(ctx context.Context, c Credentials, broadcasterID, rewardID string, in RewardInput) (*Reward, error) {
	var out struct {
		Data []Reward `json:"data"`
	}
	q := url.Values{"broadcaster_id": {broadcasterID}, "id": {rewardID}}
	if err := h.do(ctx, c, http.MethodPatch, "/channel_points/custom_rewards", q, in, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return &out.Data[0], nil
}

func (h *Helix) DeleteReward(ctx context.Context, c Credentials, broadcasterID, rewardID string) error {
	q := url.Values{"broadcaster_id": {broadcasterID}, "id": {rewardID}}
	return h.do(ctx, c, http.MethodDelete, "/channel_points/custom_rewards", q, nil, nil)
}

// ListRedemptions возвращает использования награды с заданным статусом (например "UNFULFILLED").
func (h *Helix) ListRedemptions(ctx context.Context, c Credentials, broadcasterID, rewardID, status string) ([]Redemption, error) {
	var all []Redemption
	after := ""
	for page := 0; page < 20; page++ {
		q := url.Values{
			"broadcaster_id": {broadcasterID},
			"reward_id":      {rewardID},
			"status":         {status},
			"first":          {"50"},
		}
		if after != "" {
			q.Set("after", after)
		}
		var resp struct {
			Data       []Redemption `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if err := h.do(ctx, c, http.MethodGet, "/channel_points/custom_rewards/redemptions", q, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if resp.Pagination.Cursor == "" {
			break
		}
		after = resp.Pagination.Cursor
	}
	return all, nil
}

// UpdateRedemptionStatus: FULFILLED закрывает заявку, CANCELED возвращает баллы зрителю.
// Twitch позволяет менять статус только у заявок в статусе UNFULFILLED.
func (h *Helix) UpdateRedemptionStatus(ctx context.Context, c Credentials, broadcasterID, rewardID, redemptionID, status string) error {
	q := url.Values{
		"id":             {redemptionID},
		"broadcaster_id": {broadcasterID},
		"reward_id":      {rewardID},
	}
	body := map[string]string{"status": status}
	return h.do(ctx, c, http.MethodPatch, "/channel_points/custom_rewards/redemptions", q, body, nil)
}

// CreateEventSubSubscription подписывает WebSocket-сессию на событие.
func (h *Helix) CreateEventSubSubscription(ctx context.Context, c Credentials, subType, version string, condition map[string]string, sessionID string) error {
	body := map[string]any{
		"type":      subType,
		"version":   version,
		"condition": condition,
		"transport": map[string]string{"method": "websocket", "session_id": sessionID},
	}
	return h.do(ctx, c, http.MethodPost, "/eventsub/subscriptions", nil, body, nil)
}
