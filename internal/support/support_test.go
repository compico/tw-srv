package support

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compico/tw-srv/internal/platform/sqlitedb"
)

func newTestService(t *testing.T, adminID string) *Service {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "support.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(repo, adminID)
}

func TestViewStatusIsOppositeForBothSides(t *testing.T) {
	tk := Ticket{AuthorID: "u1", Status: StatusAwaitingStaff}
	if got := tk.ViewStatus("u1"); got != "open" {
		t.Errorf("author after own message: %s", got)
	}
	if got := tk.ViewStatus("admin"); got != "waiting" {
		t.Errorf("admin after author message: %s", got)
	}
	tk.Status = StatusAwaitingAuthor
	if got := tk.ViewStatus("u1"); got != "waiting" {
		t.Errorf("author after admin reply: %s", got)
	}
	if got := tk.ViewStatus("admin"); got != "open" {
		t.Errorf("admin after own reply: %s", got)
	}
	tk.Status = StatusClosed
	if tk.ViewStatus("u1") != "closed" || tk.ViewStatus("admin") != "closed" {
		t.Error("closed must be closed for everyone")
	}
}

func TestConversationFlow(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, "admin")
	user := Viewer{ID: "u1", Name: "User"}
	admin := Viewer{ID: "admin", Name: "Admin"}
	other := Viewer{ID: "u2", Name: "Other"}

	tk, err := s.CreateTicket(ctx, user, "  Не   работает ", "Описание\r\nпроблемы")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Subject != "Не работает" || tk.Status != StatusAwaitingStaff {
		t.Fatalf("unexpected ticket: %+v", tk)
	}

	// Чужая заявка не видна.
	if _, _, err := s.Get(ctx, other, tk.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger must not see ticket, got %v", err)
	}
	if _, err := s.ListAll(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger must not list all, got %v", err)
	}

	// Ответ администратора: ждёт автора.
	if _, err := s.Reply(ctx, admin, tk.ID, "Уточните, пожалуйста"); err != nil {
		t.Fatal(err)
	}
	got, msgs, err := s.Get(ctx, user, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewStatus(user.ID) != "waiting" || got.ViewStatus(admin.ID) != "open" {
		t.Fatalf("after admin reply: user=%s admin=%s", got.ViewStatus(user.ID), got.ViewStatus(admin.ID))
	}
	if len(msgs) != 2 || msgs[0].Body != "Описание\nпроблемы" || !msgs[1].Staff {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	for _, m := range msgs {
		if m.Format != FormatMarkdown {
			t.Fatalf("new messages must be markdown, got %q", m.Format)
		}
	}

	// Ответ автора: ждёт администратора.
	if _, err := s.Reply(ctx, user, tk.ID, "Вот подробности"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Get(ctx, admin, tk.ID)
	if got.ViewStatus(admin.ID) != "waiting" || got.ViewStatus(user.ID) != "open" {
		t.Fatalf("after user reply: user=%s admin=%s", got.ViewStatus(user.ID), got.ViewStatus(admin.ID))
	}

	n, isAdmin, err := s.Summary(ctx, admin)
	if err != nil || n != 1 || !isAdmin {
		t.Fatalf("admin summary: %d %v %v", n, isAdmin, err)
	}

	// Закрыть может автор; после этого писать нельзя.
	if _, err := s.Close(ctx, user, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reply(ctx, admin, tk.ID, "ещё"); !errors.Is(err, ErrClosed) {
		t.Fatalf("reply to closed: %v", err)
	}
	if _, err := s.Close(ctx, admin, tk.ID); err != nil {
		t.Fatalf("closing twice must be harmless: %v", err)
	}
}

func TestNoAdminConfigured(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, "")
	if s.IsAdmin("") || s.IsAdmin("anyone") {
		t.Fatal("nobody is admin when ADMIN_TWITCH_ID is empty")
	}
	if _, err := s.ListAll(ctx, Viewer{ID: ""}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list all without admin: %v", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, "admin")
	u := Viewer{ID: "u1", Name: "User"}

	var ve *ValidationError
	if _, err := s.CreateTicket(ctx, u, "   ", "text"); !errors.As(err, &ve) || ve.Field != "subject" {
		t.Fatalf("empty subject: %v", err)
	}
	if _, err := s.CreateTicket(ctx, u, "тема", "   "); !errors.As(err, &ve) || ve.Field != "body" {
		t.Fatalf("empty body: %v", err)
	}
	if _, err := s.CreateTicket(ctx, u, strings.Repeat("я", MaxSubjectLen+1), "text"); !errors.As(err, &ve) || ve.Field != "subject" {
		t.Fatalf("long subject: %v", err)
	}
	if _, err := s.CreateTicket(ctx, u, "тема", strings.Repeat("я", MaxBodyLen+1)); !errors.As(err, &ve) || ve.Field != "body" {
		t.Fatalf("long body: %v", err)
	}
	for i := 0; i < MaxOpenPerAuthor; i++ {
		if _, err := s.CreateTicket(ctx, u, "тема", "text"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateTicket(ctx, u, "тема", "text"); !errors.As(err, &ve) {
		t.Fatalf("open tickets limit: %v", err)
	}
}

type fakeNotifier struct{ calls []string }

func (f *fakeNotifier) NotifyStaff(title, body, link string) {
	f.calls = append(f.calls, title+"|"+body+"|"+link)
}

func TestStaffNotifications(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, "admin")
	n := &fakeNotifier{}
	s.SetNotifier(n)
	user := Viewer{ID: "u1", Name: "User"}
	admin := Viewer{ID: "admin", Name: "Admin"}

	tk, err := s.CreateTicket(ctx, user, "Не работает", "текст")
	if err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 || !strings.HasPrefix(n.calls[0], "Новая заявка|User: Не работает|/support/") {
		t.Fatalf("new ticket must notify the admin: %v", n.calls)
	}

	if _, err := s.Reply(ctx, admin, tk.ID, "Ответ админа"); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("admin's own reply must not notify: %v", n.calls)
	}

	if _, err := s.Reply(ctx, user, tk.ID, "Ещё вопрос"); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 2 || !strings.HasPrefix(n.calls[1], "Новый ответ|User в «Не работает»|/support/") {
		t.Fatalf("author reply must notify the admin: %v", n.calls)
	}

	own, err := s.CreateTicket(ctx, admin, "Моя заявка", "текст")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reply(ctx, admin, own.ID, "ещё"); err != nil {
		t.Fatal(err)
	}
	if len(n.calls) != 2 {
		t.Fatalf("admin's own tickets must not notify: %v", n.calls)
	}
}
