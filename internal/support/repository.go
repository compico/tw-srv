package support

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if err := db.AutoMigrate(&Ticket{}, &Message{}); err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) CreateTicket(ctx context.Context, t *Ticket, first *Message) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(t).Error; err != nil {
			return err
		}
		first.TicketID = t.ID
		return tx.Create(first).Error
	})
}

func (r *Repository) GetTicket(ctx context.Context, id uint) (*Ticket, error) {
	var t Ticket
	if err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *Repository) ListMessages(ctx context.Context, ticketID uint) ([]Message, error) {
	var out []Message
	err := r.db.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("id ASC").Find(&out).Error
	return out, err
}

func (r *Repository) CountMessages(ctx context.Context, ticketID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Message{}).Where("ticket_id = ?", ticketID).Count(&n).Error
	return n, err
}

func (r *Repository) CountOpenByAuthor(ctx context.Context, authorID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Ticket{}).
		Where("author_id = ? AND status <> ?", authorID, StatusClosed).Count(&n).Error
	return n, err
}

func (r *Repository) ListByAuthor(ctx context.Context, authorID string, limit int) ([]Ticket, error) {
	var out []Ticket
	err := r.db.WithContext(ctx).Where("author_id = ?", authorID).
		Order("updated_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// ListAll для администратора: сначала те, что ждут его ответа, затем остальные открытые, затем закрытые.
func (r *Repository) ListAll(ctx context.Context, limit int) ([]Ticket, error) {
	var out []Ticket
	err := r.db.WithContext(ctx).
		Order("CASE status WHEN 'awaiting_staff' THEN 0 WHEN 'awaiting_author' THEN 1 ELSE 2 END, updated_at DESC").
		Limit(limit).Find(&out).Error
	return out, err
}

// AddMessage добавляет сообщение и переводит заявку в newStatus одной транзакцией.
// Закрытая заявка не принимает сообщений (ErrClosed), в том числе при гонке с закрытием.
// Непустой authorName обновляет имя автора заявки (ник на Twitch мог смениться).
func (r *Repository) AddMessage(ctx context.Context, ticketID uint, m *Message, newStatus Status, authorName string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"status": newStatus, "updated_at": time.Now()}
		if authorName != "" {
			upd["author_name"] = authorName
		}
		res := tx.Model(&Ticket{}).Where("id = ? AND status <> ?", ticketID, StatusClosed).Updates(upd)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrClosed
		}
		m.TicketID = ticketID
		return tx.Create(m).Error
	})
}

// CloseTicket закрывает заявку; повторное закрытие ничего не меняет.
func (r *Repository) CloseTicket(ctx context.Context, ticketID uint, byID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&Ticket{}).Where("id = ? AND status <> ?", ticketID, StatusClosed).
		Updates(map[string]any{
			"status":       StatusClosed,
			"closed_at":    now,
			"closed_by_id": byID,
			"updated_at":   now,
		}).Error
}

func (r *Repository) CountByStatusForAuthor(ctx context.Context, authorID string, st Status) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Ticket{}).Where("author_id = ? AND status = ?", authorID, st).Count(&n).Error
	return n, err
}

// CountAwaitingStaffNotBy: заявки других людей, ждущие ответа администратора.
func (r *Repository) CountAwaitingStaffNotBy(ctx context.Context, adminID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Ticket{}).
		Where("author_id <> ? AND status = ?", adminID, StatusAwaitingStaff).Count(&n).Error
	return n, err
}
