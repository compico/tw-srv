package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/fasthttp/websocket"

	"github.com/compico/overlaycomposer/internal/platform/httpx"
)

const (
	eventSubURL       = "wss://eventsub.wss.twitch.tv/ws"
	redemptionSubType = "channel.channel_points_custom_reward_redemption.add"
)

// EventSub слушает использования награды через WebSocket-транспорт Twitch.
// Публичный адрес и webhook не нужны. Одно соединение на стримера:
// Twitch ограничивает число подписок, привязанных к пользовательским токенам, на одно соединение.
type EventSub struct {
	helix *Helix
	log   *slog.Logger
	url   string
}

func NewEventSub(h *Helix, log *slog.Logger) *EventSub {
	return &EventSub{helix: h, log: log, url: eventSubURL}
}

type envelope struct {
	Metadata struct {
		MessageID   string `json:"message_id"`
		MessageType string `json:"message_type"`
	} `json:"metadata"`
	Payload json.RawMessage `json:"payload"`
}

type sessionPayload struct {
	Session struct {
		ID                      string `json:"id"`
		KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
		ReconnectURL            string `json:"reconnect_url"`
	} `json:"session"`
}

// Listen держит соединение, пока ctx не отменён, и сам переподключается с backoff.
// onEvent вызывается из цикла чтения и не должен блокироваться (тяжёлую работу выносите в горутину).
// onConnected вызывается после каждого успешного (пере)подключения и подписки;
// удобно для догонки пропущенных заявок.
// Возвращает ErrReauthRequired, если токен отозван, иначе ctx.Err().
func (e *EventSub) Listen(ctx context.Context, src TokenSource, broadcasterID, rewardID string, onEvent func(Redemption), onConnected func()) error {
	backoff := time.Second
	for {
		started := time.Now()
		err := e.session(ctx, src, broadcasterID, rewardID, onEvent, onConnected)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrReauthRequired) {
			return err
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		e.log.Warn("eventsub disconnected", "broadcaster", broadcasterID, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (e *EventSub) session(ctx context.Context, src TokenSource, broadcasterID, rewardID string, onEvent func(Redemption), onConnected func()) error {
	creds, err := src.Credentials(ctx, broadcasterID)
	if err != nil {
		return err
	}

	first, welcome, err := e.dial(ctx, e.url)
	if err != nil {
		return err
	}

	// cur указывает на активное соединение: при session_reconnect оно подменяется.
	var cur atomic.Pointer[websocket.Conn]
	cur.Store(first)
	done := make(chan struct{})
	defer close(done)
	defer func() {
		if c := cur.Load(); c != nil {
			_ = c.Close()
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			if c := cur.Load(); c != nil {
				_ = c.Close()
			}
		case <-done:
		}
	}()

	cond := map[string]string{"broadcaster_user_id": broadcasterID, "reward_id": rewardID}
	if err := e.helix.CreateEventSubSubscription(ctx, creds, redemptionSubType, "1", cond, welcome.Session.ID); err != nil {
		if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusForbidden {
			// Нет нужного scope или доступ отозван.
			return fmt.Errorf("%w: %v", ErrReauthRequired, err)
		}
		return fmt.Errorf("create subscription: %w", err)
	}
	e.log.Info("eventsub subscribed", "broadcaster", broadcasterID, "reward", rewardID)
	if onConnected != nil {
		onConnected()
	}

	timeout := keepaliveTimeout(welcome)
	seen := newSeenSet(128)
	for {
		conn := cur.Load()
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}

		switch env.Metadata.MessageType {
		case "session_keepalive":
			// Достаточно того, что дедлайн чтения продлён.

		case "notification":
			if !seen.add(env.Metadata.MessageID) {
				continue
			}
			var n struct {
				Subscription struct {
					Type string `json:"type"`
				} `json:"subscription"`
				Event json.RawMessage `json:"event"`
			}
			if json.Unmarshal(env.Payload, &n) != nil || n.Subscription.Type != redemptionSubType {
				continue
			}
			var r Redemption
			if err := json.Unmarshal(n.Event, &r); err != nil {
				e.log.Warn("eventsub: bad event", "err", err)
				continue
			}
			onEvent(r)

		case "session_reconnect":
			// Сначала подключаемся к новому адресу и только потом закрываем старое
			// соединение: подписки переезжают вместе с сессией.
			var p sessionPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil || p.Session.ReconnectURL == "" {
				return errors.New("bad session_reconnect payload")
			}
			next, nextWelcome, err := e.dial(ctx, p.Session.ReconnectURL)
			if err != nil {
				return fmt.Errorf("reconnect: %w", err)
			}
			cur.Store(next)
			_ = conn.Close()
			timeout = keepaliveTimeout(nextWelcome)

		case "revocation":
			var p struct {
				Subscription struct {
					Status string `json:"status"`
				} `json:"subscription"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			switch p.Subscription.Status {
			case "authorization_revoked", "user_removed":
				return fmt.Errorf("%w: subscription %s", ErrReauthRequired, p.Subscription.Status)
			}
			return fmt.Errorf("subscription revoked: %s", p.Subscription.Status)
		}
	}
}

// dial открывает соединение и дожидается session_welcome.
func (e *EventSub) dial(ctx context.Context, wsURL string) (*websocket.Conn, *sessionPayload, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Metadata.MessageType != "session_welcome" {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("expected session_welcome, got %q", env.Metadata.MessageType)
	}
	var p sessionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.Session.ID == "" {
		_ = conn.Close()
		return nil, nil, errors.New("bad session_welcome payload")
	}
	return conn, &p, nil
}

func keepaliveTimeout(p *sessionPayload) time.Duration {
	s := p.Session.KeepaliveTimeoutSeconds
	if s <= 0 {
		s = 10
	}
	return time.Duration(s+5) * time.Second
}

// seenSet помнит последние message_id: Twitch может доставить одно уведомление дважды.
type seenSet struct {
	ids []string
	set map[string]struct{}
	max int
}

func newSeenSet(max int) *seenSet {
	return &seenSet{set: make(map[string]struct{}, max), max: max}
}

// add возвращает false, если id уже встречался.
func (s *seenSet) add(id string) bool {
	if id == "" {
		return true
	}
	if _, ok := s.set[id]; ok {
		return false
	}
	s.set[id] = struct{}{}
	s.ids = append(s.ids, id)
	if len(s.ids) > s.max {
		delete(s.set, s.ids[0])
		s.ids = s.ids[1:]
	}
	return true
}
