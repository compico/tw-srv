// Package support: обратная связь. Заявка (Ticket) это переписка (Message) между автором и администратором.
package support

import "time"

// Status хранится «объективно»: кто должен ответить следующим. Для каждого из участников
// «открыто» и «ждёт ответа» противоположны (см. Ticket.ViewStatus).
type Status string

const (
	StatusAwaitingStaff  Status = "awaiting_staff"  // последним написал автор: ответа ждёт администратор
	StatusAwaitingAuthor Status = "awaiting_author" // последним написал администратор: ответа ждёт автор
	StatusClosed         Status = "closed"
)

// Форматы текста сообщения. Сейчас только простой текст; markdown добавится новым значением,
// старые сообщения останутся как есть.
const FormatText = "text"

type Ticket struct {
	ID         uint   `gorm:"primaryKey"`
	AuthorID   string `gorm:"index;not null"` // Twitch user id
	AuthorName string
	Subject    string `gorm:"not null"`
	Status     Status `gorm:"index;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClosedAt   *time.Time
	ClosedByID string
}

func (Ticket) TableName() string { return "support_tickets" }

type Message struct {
	ID         uint   `gorm:"primaryKey"`
	TicketID   uint   `gorm:"index;not null"`
	AuthorID   string `gorm:"not null"`
	AuthorName string
	Staff      bool   // написано администратором в чужой заявке
	Format     string `gorm:"not null;default:text"`
	Body       string `gorm:"not null"`
	CreatedAt  time.Time
}

func (Message) TableName() string { return "support_messages" }

// ViewStatus: статус глазами участника.
//
//	closed  закрыто;
//	waiting ждёт ответа именно от этого участника;
//	open    участник ответил (или заявку только что создал), ждёт другую сторону.
func (t Ticket) ViewStatus(viewerID string) string {
	if t.Status == StatusClosed {
		return "closed"
	}
	if viewerID == t.AuthorID {
		if t.Status == StatusAwaitingAuthor {
			return "waiting"
		}
		return "open"
	}
	if t.Status == StatusAwaitingStaff {
		return "waiting"
	}
	return "open"
}
