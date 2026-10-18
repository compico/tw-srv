package support

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

const (
	MaxSubjectLen     = 120
	MaxBodyLen        = 5000
	MaxOpenPerAuthor  = 10
	MaxMessagesPerTkt = 500
	listLimit         = 200
)

var (
	ErrNotFound = errors.New("заявка не найдена")
	ErrClosed   = errors.New("заявка закрыта")
)

// ValidationError: ошибка пользовательского ввода (Field пустой для общих ограничений).
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Viewer: кто выполняет действие (из сессии).
type Viewer struct {
	ID   string
	Name string
}

// Notifier получает уведомления для администратора (например, браузерные push).
// Вызывается после успешной записи; реализация не должна блокировать запрос.
type Notifier interface {
	NotifyStaff(title, body, link string)
}

type Service struct {
	repo     *Repository
	adminID  string
	notifier Notifier
}

// SetNotifier подключает уведомления; без него сервис просто ничего не рассылает.
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

func (s *Service) notifyStaff(actorID, title, body, link string) {
	// Свои же действия администратора его не уведомляют.
	if s.notifier == nil || s.IsAdmin(actorID) {
		return
	}
	s.notifier.NotifyStaff(title, body, link)
}

func ticketLink(id uint) string { return "/support/" + strconv.FormatUint(uint64(id), 10) }

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// NewService: adminID это Twitch user id администратора. Пустой id означает, что администратора нет
// и доступа ко всем заявкам нет ни у кого.
func NewService(repo *Repository, adminID string) *Service {
	return &Service{repo: repo, adminID: strings.TrimSpace(adminID)}
}

func (s *Service) IsAdmin(userID string) bool {
	return s.adminID != "" && userID == s.adminID
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func validateBody(body string) (string, error) {
	body = cleanText(body)
	if body == "" {
		return "", &ValidationError{Field: "body", Message: "Введите сообщение"}
	}
	if utf8.RuneCountInString(body) > MaxBodyLen {
		return "", &ValidationError{Field: "body", Message: "Сообщение слишком длинное"}
	}
	return body, nil
}

func (s *Service) CreateTicket(ctx context.Context, v Viewer, subject, body string) (*Ticket, error) {
	subject = strings.Join(strings.Fields(subject), " ")
	if subject == "" {
		return nil, &ValidationError{Field: "subject", Message: "Введите тему"}
	}
	if utf8.RuneCountInString(subject) > MaxSubjectLen {
		return nil, &ValidationError{Field: "subject", Message: "Тема слишком длинная"}
	}
	body, err := validateBody(body)
	if err != nil {
		return nil, err
	}
	open, err := s.repo.CountOpenByAuthor(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	if open >= MaxOpenPerAuthor {
		return nil, &ValidationError{Message: "Слишком много открытых заявок: закройте ненужные"}
	}
	t := &Ticket{AuthorID: v.ID, AuthorName: v.Name, Subject: subject, Status: StatusAwaitingStaff}
	m := &Message{AuthorID: v.ID, AuthorName: v.Name, Format: FormatMarkdown, Body: body}
	if err := s.repo.CreateTicket(ctx, t, m); err != nil {
		return nil, err
	}
	s.notifyStaff(v.ID, "Новая заявка", v.Name+": "+shorten(subject, 100), ticketLink(t.ID))
	return t, nil
}

// ListMine: заявки самого пользователя.
func (s *Service) ListMine(ctx context.Context, v Viewer) ([]Ticket, error) {
	return s.repo.ListByAuthor(ctx, v.ID, listLimit)
}

// ListAll: все заявки; только для администратора.
func (s *Service) ListAll(ctx context.Context, v Viewer) ([]Ticket, error) {
	if !s.IsAdmin(v.ID) {
		return nil, ErrNotFound
	}
	return s.repo.ListAll(ctx, listLimit)
}

// access возвращает заявку, если её автор или администратор; иначе ErrNotFound (существование не раскрываем).
func (s *Service) access(ctx context.Context, v Viewer, id uint) (*Ticket, error) {
	t, err := s.repo.GetTicket(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if t.AuthorID != v.ID && !s.IsAdmin(v.ID) {
		return nil, ErrNotFound
	}
	return t, nil
}

func (s *Service) Get(ctx context.Context, v Viewer, id uint) (*Ticket, []Message, error) {
	t, err := s.access(ctx, v, id)
	if err != nil {
		return nil, nil, err
	}
	msgs, err := s.repo.ListMessages(ctx, t.ID)
	if err != nil {
		return nil, nil, err
	}
	return t, msgs, nil
}

// Reply: ответ автора переводит заявку в «ждёт администратора», ответ администратора в «ждёт автора».
func (s *Service) Reply(ctx context.Context, v Viewer, id uint, body string) (*Ticket, error) {
	t, err := s.access(ctx, v, id)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusClosed {
		return nil, ErrClosed
	}
	body, err = validateBody(body)
	if err != nil {
		return nil, err
	}
	n, err := s.repo.CountMessages(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if n >= MaxMessagesPerTkt {
		return nil, &ValidationError{Message: "В заявке слишком много сообщений: создайте новую"}
	}

	isAuthor := v.ID == t.AuthorID
	next, name := StatusAwaitingAuthor, ""
	if isAuthor {
		next, name = StatusAwaitingStaff, v.Name
	}
	m := &Message{AuthorID: v.ID, AuthorName: v.Name, Staff: !isAuthor, Format: FormatMarkdown, Body: body}
	if err := s.repo.AddMessage(ctx, t.ID, m, next, name); err != nil {
		return nil, err
	}
	if isAuthor {
		s.notifyStaff(v.ID, "Новый ответ", v.Name+" в «"+shorten(t.Subject, 80)+"»", ticketLink(t.ID))
	}
	return s.repo.GetTicket(ctx, t.ID)
}

// Close: закрыть может автор или администратор.
func (s *Service) Close(ctx context.Context, v Viewer, id uint) (*Ticket, error) {
	t, err := s.access(ctx, v, id)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CloseTicket(ctx, t.ID, v.ID); err != nil {
		return nil, err
	}
	return s.repo.GetTicket(ctx, t.ID)
}

// Summary: сколько заявок ждёт ответа именно от этого пользователя (для бейджа в панели).
func (s *Service) Summary(ctx context.Context, v Viewer) (awaiting int64, isAdmin bool, err error) {
	awaiting, err = s.repo.CountByStatusForAuthor(ctx, v.ID, StatusAwaitingAuthor)
	if err != nil {
		return 0, false, err
	}
	isAdmin = s.IsAdmin(v.ID)
	if isAdmin {
		n, err := s.repo.CountAwaitingStaffNotBy(ctx, v.ID)
		if err != nil {
			return 0, false, err
		}
		awaiting += n
	}
	return awaiting, isAdmin, nil
}
