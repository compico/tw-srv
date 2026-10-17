package auth

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
	if err := db.AutoMigrate(&User{}, &Token{}, &Session{}, &LoginState{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) UpsertUser(ctx context.Context, u *User) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(u).Error
}

func (r *Repository) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repository) SaveToken(ctx context.Context, t *Token) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(t).Error
}

func (r *Repository) GetToken(ctx context.Context, userID string) (*Token, error) {
	var t Token
	if err := r.db.WithContext(ctx).First(&t, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *Repository) MarkTokenInvalid(ctx context.Context, userID, reason string) error {
	return r.db.WithContext(ctx).Model(&Token{}).Where("user_id = ?", userID).
		Updates(map[string]any{"invalid": true, "invalid_reason": reason, "updated_at": time.Now()}).Error
}

func (r *Repository) TouchValidated(ctx context.Context, userID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&Token{}).Where("user_id = ?", userID).
		Update("last_validated_at", at).Error
}

// TokensDue возвращает рабочие токены, которым пора обновиться или пройти validate.
func (r *Repository) TokensDue(ctx context.Context, expiresBefore, validatedBefore time.Time) ([]Token, error) {
	var out []Token
	err := r.db.WithContext(ctx).
		Where("invalid = ? AND (expires_at < ? OR last_validated_at < ?)", false, expiresBefore, validatedBefore).
		Find(&out).Error
	return out, err
}

func (r *Repository) CreateSession(ctx context.Context, s *Session) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *Repository) GetSession(ctx context.Context, idHash string) (*Session, error) {
	var s Session
	if err := r.db.WithContext(ctx).First(&s, "id_hash = ?", idHash).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repository) DeleteSession(ctx context.Context, idHash string) error {
	return r.db.WithContext(ctx).Delete(&Session{}, "id_hash = ?", idHash).Error
}

func (r *Repository) SaveState(ctx context.Context, s *LoginState) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// TakeState достаёт и сразу удаляет state (одноразовый).
func (r *Repository) TakeState(ctx context.Context, state string) (*LoginState, error) {
	var ls LoginState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&ls, "state = ?", state).Error; err != nil {
			return err
		}
		return tx.Delete(&LoginState{}, "state = ?", state).Error
	})
	if err != nil {
		return nil, err
	}
	return &ls, nil
}

func (r *Repository) DeleteExpired(ctx context.Context, now time.Time) error {
	if err := r.db.WithContext(ctx).Delete(&Session{}, "expires_at < ?", now).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Delete(&LoginState{}, "expires_at < ?", now).Error
}
