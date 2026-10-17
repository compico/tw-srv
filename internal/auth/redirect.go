package auth

import (
	"net/url"
	"strings"
)

// SafeReturn выбирает адрес возврата после входа (защита от open redirect).
// Абсолютный адрес допустим, только если его origin входит в allowed.
// Локальный путь допустим, только если сервисы работают на одном домене (def пуст).
// Иначе возвращается def (или "/", если он не задан).
func SafeReturn(p string, allowed []string, def string) string {
	fallback := def
	if fallback == "" {
		fallback = "/"
	}
	if p == "" {
		return fallback
	}
	if strings.HasPrefix(p, "/") {
		if def == "" && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `/\`) {
			return p
		}
		return fallback
	}
	u, err := url.Parse(p)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fallback
	}
	origin := u.Scheme + "://" + u.Host
	for _, o := range allowed {
		if strings.EqualFold(o, origin) {
			return u.String()
		}
	}
	return fallback
}
