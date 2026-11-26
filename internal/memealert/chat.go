package memealert

// ChatEvent: итог обработки заявки, о котором можно написать в чат стримера.
type ChatEvent struct {
	StreamerID  string // id канала Twitch
	Success     bool   // true: мемкоины выданы; false: баллы возвращены
	ViewerName  string
	ViewerLogin string
	Nick        string // ник, который ввёл зритель, или найденный ник саппортера
	Amount      int
	Reason      string // для неудачи: причина, безопасная для публикации в чате
}

// ChatNotifier отправляет сообщения в чат. Реализация не должна блокировать и не должна влиять на выдачу.
type ChatNotifier interface {
	Notify(ev ChatEvent)
}

// SetChat подключает отправку сообщений в чат (необязательно).
func (s *Service) SetChat(n ChatNotifier) { s.chat = n }

// notifyChat вызывается только после окончательного исхода (выдано или возвращено).
// Любая ошибка или паника уведомителя не должна ломать обработку заявки.
func (s *Service) notifyChat(rw *Reward, rd *Redemption, matchedNick string, success bool, publicReason string) {
	if s.chat == nil {
		return
	}
	nick := matchedNick
	if nick == "" {
		nick = normalizeNick(rd.UserInput)
	}
	ev := ChatEvent{
		StreamerID:  rw.TwitchUserID,
		Success:     success,
		ViewerName:  rd.ViewerName,
		ViewerLogin: rd.ViewerLogin,
		Nick:        nick,
		Amount:      rw.BonusAmount,
		Reason:      publicReason,
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("chat notify panic", "recover", r)
		}
	}()
	s.chat.Notify(ev)
}
