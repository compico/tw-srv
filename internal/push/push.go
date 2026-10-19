// Package push: браузерные push-уведомления (Web Push, VAPID) для администратора поддержки.
// Подписка создаётся в браузере (Push API + service worker), сервер хранит её и шлёт события.
package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxSubsPerUser = 10 // устройств на пользователя
	sendTimeout    = 20 * time.Second
	ttlSeconds     = 24 * 60 * 60 // сколько push-сервис держит сообщение, если браузер офлайн
)

var (
	ErrDisabled        = errors.New("push-уведомления не настроены: нет VAPID-ключей")
	ErrBadSubscription = errors.New("некорректная подписка")
	ErrTooMany         = errors.New("слишком много подписок: отключите ненужные устройства")
)

type Subscription struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    string `gorm:"index;not null;size:32"` // Twitch user id
	Endpoint  string `gorm:"uniqueIndex;not null;size:2048"`
	P256dh    string `gorm:"not null"`
	Auth      string `gorm:"not null"`
	UserAgent string
	CreatedAt time.Time
}

func (Subscription) TableName() string { return "push_subscriptions" }

// SubscriptionInput: результат PushSubscription.toJSON() в браузере.
type SubscriptionInput struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// ---------------------------------------------------------------- база

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) (*Repository, error) {
	if err := db.AutoMigrate(&Subscription{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) upsert(ctx context.Context, sub *Subscription) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "endpoint"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "p256dh", "auth", "user_agent"}),
	}).Create(sub).Error
}

func (r *Repository) listByUser(ctx context.Context, userID string) ([]Subscription, error) {
	var out []Subscription
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&out).Error
	return out, err
}

func (r *Repository) countByUser(ctx context.Context, userID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Subscription{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

func (r *Repository) exists(ctx context.Context, endpoint string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Subscription{}).Where("endpoint = ?", endpoint).Count(&n).Error
	return n > 0, err
}

func (r *Repository) deleteOwned(ctx context.Context, userID, endpoint string) error {
	return r.db.WithContext(ctx).Delete(&Subscription{}, "user_id = ? AND endpoint = ?", userID, endpoint).Error
}

func (r *Repository) deleteEndpoint(ctx context.Context, endpoint string) error {
	return r.db.WithContext(ctx).Delete(&Subscription{}, "endpoint = ?", endpoint).Error
}

// ---------------------------------------------------------------- сервис

type Config struct {
	PublicKey  string // VAPID, base64url
	PrivateKey string
	Subject    string // mailto: или https://: контакт для push-сервисов
	AdminID    string // кому идут уведомления о заявках
}

type Service struct {
	repo      *Repository
	cfg       Config
	log       *slog.Logger
	allowHTTP bool // только для тестов: endpoint по http
}

func NewService(repo *Repository, cfg Config, log *slog.Logger) *Service {
	return &Service{repo: repo, cfg: cfg, log: log}
}

func (s *Service) Enabled() bool     { return s.cfg.PublicKey != "" && s.cfg.PrivateKey != "" }
func (s *Service) PublicKey() string { return s.cfg.PublicKey }
func (s *Service) IsAdmin(userID string) bool {
	return s.cfg.AdminID != "" && userID == s.cfg.AdminID
}

func b64Len(s string, want int) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == want
}

func (s *Service) validate(in SubscriptionInput) error {
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Host == "" || len(in.Endpoint) > 2048 {
		return ErrBadSubscription
	}
	if u.Scheme != "https" && !(s.allowHTTP && u.Scheme == "http") {
		return ErrBadSubscription
	}
	// p256dh: несжатая точка P-256 (65 байт), auth: 16 байт.
	if !b64Len(in.Keys.P256dh, 65) || !b64Len(in.Keys.Auth, 16) {
		return ErrBadSubscription
	}
	return nil
}

func (s *Service) Subscribe(ctx context.Context, userID string, in SubscriptionInput, userAgent string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if err := s.validate(in); err != nil {
		return err
	}
	known, err := s.repo.exists(ctx, in.Endpoint)
	if err != nil {
		return err
	}
	if !known {
		n, err := s.repo.countByUser(ctx, userID)
		if err != nil {
			return err
		}
		if n >= maxSubsPerUser {
			return ErrTooMany
		}
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	return s.repo.upsert(ctx, &Subscription{
		UserID: userID, Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth, UserAgent: userAgent,
	})
}

func (s *Service) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	return s.repo.deleteOwned(ctx, userID, endpoint)
}

func (s *Service) Count(ctx context.Context, userID string) (int64, error) {
	return s.repo.countByUser(ctx, userID)
}

// Send отправляет уведомление на все устройства пользователя и возвращает число принятых push-сервисом.
// Подписки, которые сервис признал мёртвыми (404/410), удаляются.
func (s *Service) Send(ctx context.Context, userID string, p Payload) int {
	if !s.Enabled() {
		return 0
	}
	subs, err := s.repo.listByUser(ctx, userID)
	if err != nil {
		s.log.Error("push: list subscriptions", "err", err)
		return 0
	}
	msg, _ := json.Marshal(p)
	sent := 0
	for _, sub := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, msg, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			Subscriber:      s.cfg.Subject,
			VAPIDPublicKey:  s.cfg.PublicKey,
			VAPIDPrivateKey: s.cfg.PrivateKey,
			TTL:             ttlSeconds,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			s.log.Warn("push: send", "err", err)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode == 404 || resp.StatusCode == 410:
			if err := s.repo.deleteEndpoint(ctx, sub.Endpoint); err != nil {
				s.log.Error("push: delete dead subscription", "err", err)
			}
		case resp.StatusCode >= 300:
			s.log.Warn("push: service rejected notification", "status", resp.StatusCode)
		default:
			sent++
		}
	}
	return sent
}

// NotifyStaff реализует support.Notifier: уведомление администратору. Запрос поддержки не ждёт отправки.
func (s *Service) NotifyStaff(title, body, link string) {
	if !s.Enabled() || s.cfg.AdminID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		s.Send(ctx, s.cfg.AdminID, Payload{Title: title, Body: body, URL: link, Tag: link})
	}()
}
