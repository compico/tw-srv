package chatbot

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type Repository struct{ db *gorm.DB }

// NewRepository только добавляет новые таблицы: существующие данные не затрагиваются.
func NewRepository(db *gorm.DB) (*Repository, error) {
	if err := db.AutoMigrate(&Bot{}, &Settings{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) GetBot(ctx context.Context, streamerID string) (*Bot, error) {
	var b Bot
	err := r.db.WithContext(ctx).First(&b, "twitch_user_id = ?", streamerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &b, err
}

func (r *Repository) SaveBot(ctx context.Context, b *Bot) error {
	return r.db.WithContext(ctx).Save(b).Error
}

func (r *Repository) DeleteBot(ctx context.Context, streamerID string) error {
	return r.db.WithContext(ctx).Delete(&Bot{}, "twitch_user_id = ?", streamerID).Error
}

func (r *Repository) UpdateBot(ctx context.Context, streamerID string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&Bot{}).Where("twitch_user_id = ?", streamerID).Updates(fields).Error
}

// GetSettings возвращает nil, если стример ещё ничего не сохранял.
func (r *Repository) GetSettings(ctx context.Context, streamerID string) (*Settings, error) {
	var s Settings
	err := r.db.WithContext(ctx).First(&s, "twitch_user_id = ?", streamerID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

func (r *Repository) SaveSettings(ctx context.Context, s *Settings) error {
	return r.db.WithContext(ctx).Save(s).Error
}
