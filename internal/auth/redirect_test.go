package auth

import "testing"

func TestSafeReturn(t *testing.T) {
	allowed := []string{"https://app.example.com"}
	const def = "https://app.example.com/"

	cases := []struct {
		name, in, allowedDef, want string
		useAllowed                 bool
	}{
		{"empty -> default", "", def, def, true},
		{"allowed origin with path", "https://app.example.com/settings?x=1", def, "https://app.example.com/settings?x=1", true},
		{"allowed origin root", "https://app.example.com/", def, "https://app.example.com/", true},
		{"case-insensitive host", "https://APP.example.com/a", def, "https://APP.example.com/a", true},
		{"foreign origin", "https://evil.com/", def, def, true},
		{"lookalike suffix", "https://app.example.com.evil.com/", def, def, true},
		{"userinfo trick", "https://app.example.com@evil.com/", def, def, true},
		{"scheme mismatch", "http://app.example.com/", def, def, true},
		{"protocol-relative", "//evil.com", def, def, true},
		{"javascript scheme", "javascript:alert(1)", def, def, true},
		{"local path ignored when default set", "/settings", def, def, true},
		{"single-domain: local path ok", "/settings", "", "/settings", false},
		{"single-domain: protocol-relative blocked", "//evil.com", "", "/", false},
		{"single-domain: backslash blocked", `/\evil.com`, "", "/", false},
		{"single-domain: absolute blocked", "https://evil.com/", "", "/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var a []string
			if c.useAllowed {
				a = allowed
			}
			if got := SafeReturn(c.in, a, c.allowedDef); got != c.want {
				t.Fatalf("SafeReturn(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
