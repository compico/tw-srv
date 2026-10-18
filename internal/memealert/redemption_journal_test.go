package memealert

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/compico/tw-srv/internal/platform/sqlitedb"
)

// Поля, которые сервис пишет в журнал перед выдачей бонуса, должны существовать в таблице.
// Раньше имя колонки не совпадало с моделью, и запись состояния giving падала.
func TestGivingFieldsMatchRedemptionTable(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "memealerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	rd := &Redemption{ID: "r1", TwitchUserID: "u1", Status: StatusReceived}
	if created, err := repo.CreateRedemption(ctx, rd); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}

	sup := Supporter{SupporterID: "ma-123", SupporterName: "Чинила"}
	if err := repo.UpdateRedemption(ctx, "r1", givingFields(sup)); err != nil {
		t.Fatalf("persist giving state: %v", err)
	}
	got, err := repo.GetRedemption(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusGiving || got.MatchedNick != "Чинила" || got.MemeAlertsUserID != "ma-123" {
		t.Fatalf("unexpected redemption: %+v", got)
	}
}
