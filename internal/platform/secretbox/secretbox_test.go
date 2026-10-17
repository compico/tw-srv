package secretbox

import (
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("refresh-token-123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "refresh-token") {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := box.Open(sealed)
	if err != nil || got != "refresh-token-123" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestOpenRejectsTamperedData(t *testing.T) {
	box, _ := New(strings.Repeat("ab", 32))
	sealed, _ := box.Seal("x")
	other, _ := New(strings.Repeat("cd", 32))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("expected error with wrong key")
	}
}

func TestBadKey(t *testing.T) {
	if _, err := New("short"); err == nil {
		t.Fatal("expected error for short key")
	}
}
