package memealert

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if err := db.AutoMigrate(&Account{}, &Reward{}, &Redemption{}, &OAuthState{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

// ---- Account ----

func (r *Repository) GetAccount(ctx context.Context, twitchUserID string) (*Account, error) {
	var a Account
	if err := r.db.WithContext(ctx).First(&a, "twitch_user_id = ?", twitchUserID).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repository) SaveAccount(ctx context.Context, a *Account) error {
	return r.db.WithContext(ctx).Save(a).Error
}

func (r *Repository) UpdateAccountTokens(ctx context.Context, twitchUserID, accessEnc, refreshEnc string, expiresAt time.Time) error {
	return r.db.WithContext(ctx).Model(&Account{}).Where("twitch_user_id = ?", twitchUserID).Updates(map[string]any{
		"access_token_enc":  accessEnc,
		"refresh_token_enc": refreshEnc,
		"expires_at":        expiresAt,
	}).Error
}

func (r *Repository) SetAccountInvalid(ctx context.Context, twitchUserID string, invalid bool) error {
	return r.db.WithContext(ctx).Model(&Account{}).Where("twitch_user_id = ?", twitchUserID).
		Update("invalid", invalid).Error
}

func (r *Repository) SetTwitchReauth(ctx context.Context, twitchUserID string, v bool) error {
	return r.db.WithContext(ctx).Model(&Account{}).Where("twitch_user_id = ?", twitchUserID).
		Update("twitch_reauth", v).Error
}

func (r *Repository) DeleteAccount(ctx context.Context, twitchUserID string) error {
	return r.db.WithContext(ctx).Delete(&Account{}, "twitch_user_id = ?", twitchUserID).Error
}

// ---- Reward ----

func (r *Repository) GetReward(ctx context.Context, twitchUserID string) (*Reward, error) {
	var rw Reward
	if err := r.db.WithContext(ctx).First(&rw, "twitch_user_id = ?", twitchUserID).Error; err != nil {
		return nil, err
	}
	return &rw, nil
}

func (r *Repository) GetRewardByTwitchID(ctx context.Context, twitchRewardID string) (*Reward, error) {
	var rw Reward
	if err := r.db.WithContext(ctx).First(&rw, "twitch_reward_id = ?", twitchRewardID).Error; err != nil {
		return nil, err
	}
	return &rw, nil
}

func (r *Repository) SaveReward(ctx context.Context, rw *Reward) error {
	return r.db.WithContext(ctx).Save(rw).Error
}

func (r *Repository) DeleteReward(ctx context.Context, twitchUserID string) error {
	return r.db.WithContext(ctx).Delete(&Reward{}, "twitch_user_id = ?", twitchUserID).Error
}

// ListActiveRewards: награды, для которых нужно слушать события.
func (r *Repository) ListActiveRewards(ctx context.Context) ([]Reward, error) {
	var out []Reward
	err := r.db.WithContext(ctx).Where("enabled = ? AND twitch_reward_id <> ''", true).Find(&out).Error
	return out, err
}

// ---- Redemption ----

// CreateRedemption возвращает created=false, если заявка с таким id уже есть.
func (r *Repository) CreateRedemption(ctx context.Context, rd *Redemption) (created bool, err error) {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(rd)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *Repository) GetRedemption(ctx context.Context, id string) (*Redemption, error) {
	var rd Redemption
	if err := r.db.WithContext(ctx).First(&rd, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &rd, nil
}

func (r *Repository) UpdateRedemption(ctx context.Context, id string, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&Redemption{}).Where("id = ?", id).Updates(fields).Error
}

func (r *Repository) ListRedemptions(ctx context.Context, twitchUserID string, limit int) ([]Redemption, error) {
	var out []Redemption
	err := r.db.WithContext(ctx).Where("twitch_user_id = ?", twitchUserID).
		Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// MarkInterruptedGiving: заявки, застрявшие в giving после падения процесса.
// Мы не знаем, дошёл ли запрос на выдачу бонуса, поэтому автоматически не повторяем
// (риск двойного начисления) и не возвращаем баллы, а помечаем для ручной проверки.
func (r *Repository) MarkInterruptedGiving(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&Redemption{}).Where("status = ?", StatusGiving).Updates(map[string]any{
		"status": StatusNeedsReview,
		"reason": "Сервис был перезапущен во время выдачи бонуса: проверьте начисление вручную",
	})
	return res.RowsAffected, res.Error
}

// ---- OAuth state ----

func (r *Repository) SaveOAuthState(ctx context.Context, s *OAuthState) error {
	_ = r.db.WithContext(ctx).Delete(&OAuthState{}, "expires_at < ?", time.Now()).Error
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *Repository) TakeOAuthState(ctx context.Context, state string) (*OAuthState, error) {
	var st OAuthState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&st, "state = ?", state).Error; err != nil {
			return err
		}
		return tx.Delete(&OAuthState{}, "state = ?", state).Error
	})
	if err != nil {
		return nil, err
	}
	return &st, nil
}
