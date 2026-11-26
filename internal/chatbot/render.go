package chatbot

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/compico/tw-srv/internal/memealert"
)

// Переменные шаблона: {user} имя зрителя, {nick} ник MemeAlerts, {amount} число мемкоинов,
// {reason} причина возврата (только для сообщения о неудаче).
var Variables = []string{"{user}", "{nick}", "{amount}", "{reason}"}

// clean убирает управляющие символы и переводы строк и обрезает значение, пришедшее от зрителя.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '​' {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// Render подставляет значения в шаблон и приводит результат к виду, безопасному для чата:
// одна строка, не длиннее лимита Twitch, без ведущих «/» и «.» (чтобы значение не стало командой).
func Render(tpl string, ev memealert.ChatEvent) string {
	name := ev.ViewerName
	if name == "" {
		name = ev.ViewerLogin
	}
	out := strings.NewReplacer(
		"{user}", clean(name, 40),
		"{nick}", clean(ev.Nick, 40),
		"{amount}", strconv.Itoa(ev.Amount),
		"{reason}", clean(ev.Reason, 200),
	).Replace(tpl)
	out = clean(out, MaxMessageRunes)
	return strings.TrimLeft(out, "/. ")
}
