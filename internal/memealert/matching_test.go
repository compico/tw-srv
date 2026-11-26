package memealert

import (
	"strings"
	"testing"
)

const (
	idA = "5f1a2b3c4d5e6f7a8b9c0d1e"
	idB = "6a1a2b3c4d5e6f7a8b9c0d1e"
)

func TestIsSupporterID(t *testing.T) {
	for in, want := range map[string]bool{
		idA:                        true,
		strings.ToUpper(idA):       true,
		"Чинила":                   false,
		"bob":                      false,
		idA + "0":                  false, // 25 символов
		idA[:23]:                   false,
		"zz1a2b3c4d5e6f7a8b9c0d1e": false, // не hex
	} {
		if got := isSupporterID(in); got != want {
			t.Errorf("isSupporterID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMatchesInput(t *testing.T) {
	sp := Supporter{SupporterID: idA, SupporterName: "Bob", SupporterLink: "bob_link"}
	for _, nick := range []string{"bob", "BOB", "Bob_Link", idA, strings.ToUpper(idA)} {
		if !matchesInput(sp, nick) {
			t.Errorf("%q должен подходить", nick)
		}
	}
	// Для обычного ника id саппортера значения не имеет.
	if matchesInput(Supporter{SupporterID: "abc", SupporterName: "x"}, "abc") {
		t.Error("id не должен сравниваться с не-id вводом")
	}
	if matchesInput(sp, idB) {
		t.Error("чужой id не подходит")
	}
}

func TestPickSupporterUnique(t *testing.T) {
	sp, res, dupes := pickSupporter([]Supporter{{SupporterID: idA, SupporterName: "Bob"}}, "bob")
	if res != matchFound || sp.SupporterID != idA || dupes != nil {
		t.Fatalf("got %+v %v %v", sp, res, dupes)
	}
}

func TestPickSupporterNone(t *testing.T) {
	if _, res, _ := pickSupporter(nil, "bob"); res != matchNone {
		t.Fatalf("res = %v", res)
	}
}

func TestPickSupporterMuted(t *testing.T) {
	_, res, _ := pickSupporter([]Supporter{{SupporterID: idA, SupporterName: "Bob", MutedByStreamer: true}}, "bob")
	if res != matchMuted {
		t.Fatalf("res = %v", res)
	}
}

// Два разных саппортера с одним ником: отказ и список кандидатов (как и раньше, без выдачи бонуса).
func TestPickSupporterDuplicateNames(t *testing.T) {
	found := []Supporter{
		{SupporterID: idA, SupporterName: "Bob", SupporterLink: "bob1"},
		{SupporterID: idB, SupporterName: "bob", SupporterLink: "bob2"},
		{SupporterID: idA, SupporterName: "Bob", SupporterLink: "bob1"}, // тот же человек повторно: не дубликат
	}
	_, res, dupes := pickSupporter(found, "bob")
	if res != matchAmbiguous {
		t.Fatalf("res = %v", res)
	}
	if len(dupes) != 2 || dupes[0].SupporterID != idA || dupes[1].SupporterID != idB {
		t.Fatalf("dupes = %+v", dupes)
	}
}

// Ввод в виде id однозначно выбирает человека, даже если у его имени есть тёзки.
func TestPickSupporterByIDResolvesDuplicates(t *testing.T) {
	found := []Supporter{
		{SupporterID: idA, SupporterName: idB}, // имя другого пользователя совпало с чужим id: не важно
		{SupporterID: idB, SupporterName: "bob"},
	}
	sp, res, _ := pickSupporter(found, idB)
	if res != matchFound || sp.SupporterID != idB {
		t.Fatalf("got %+v %v", sp, res)
	}
}

func TestPickSupporterByIDMuted(t *testing.T) {
	found := []Supporter{{SupporterID: idA, SupporterName: "bob", MutedByStreamer: true}, {SupporterID: idB, SupporterName: "bob"}}
	_, res, _ := pickSupporter(found, idA)
	if res != matchMuted {
		t.Fatalf("res = %v", res)
	}
}

func TestAmbiguousReason(t *testing.T) {
	dupes := []Supporter{
		{SupporterID: idA, SupporterName: "Bob", SupporterLink: "bob1"},
		{SupporterID: idB, SupporterName: "bob"},
	}
	got := ambiguousReason("bob", dupes, true)
	for _, want := range []string{"«bob»", idA, idB, "memealerts.com/bob1", "ID или ссылку"} {
		if !strings.Contains(got, want) {
			t.Errorf("причина %q не содержит %q", got, want)
		}
	}
	if !strings.Contains(ambiguousReason("bob", dupes, false), "Включите у награды ввод ника") {
		t.Error("без ввода ника нужна подсказка включить его")
	}

	many := make([]Supporter, 8)
	for i := range many {
		many[i] = Supporter{SupporterID: idA, SupporterName: "bob"}
	}
	if !strings.Contains(ambiguousReason("bob", many, true), "и ещё 3") {
		t.Error("длинный список должен сокращаться")
	}
}

func TestNormalizeNickKeepsID(t *testing.T) {
	if got := normalizeNick(" @" + idA + " "); got != idA {
		t.Fatalf("got %q", got)
	}
	if got := normalizeNick("https://memealerts.com/bob_link/"); got != "bob_link" {
		t.Fatalf("got %q", got)
	}
}
