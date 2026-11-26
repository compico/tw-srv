package memealert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/compico/tw-srv/internal/platform/httpx"
	"github.com/compico/tw-srv/internal/twitch"
)

const (
	supporterPageSize = 100
	supporterMaxPages = 10
)

type matchResult int

const (
	matchFound matchResult = iota
	matchNone
	matchAmbiguous
	matchMuted
)

// HandleRedemption обрабатывает одну заявку на награду: ищет ник среди саппортеров,
// выдаёт бонус и закрывает заявку, либо возвращает баллы зрителю.
//
// Гарантии:
//   - одна заявка Twitch обрабатывается не более одного раза (журнал + PRIMARY KEY);
//   - заявки одного стримера идут строго по очереди;
//   - если исход выдачи бонуса неизвестен (таймаут, 5xx), баллы НЕ возвращаются
//     и бонус НЕ повторяется: заявка уходит в needs_review.
func (s *Service) HandleRedemption(ctx context.Context, ev twitch.Redemption) {
	rw, err := s.repo.GetRewardByTwitchID(ctx, ev.Reward.ID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			s.log.Error("load reward", "reward", ev.Reward.ID, "err", err)
		}
		return
	}
	if !strings.EqualFold(ev.Status, "unfulfilled") {
		return
	}

	mu := lockFor(&s.streamerLocks, rw.TwitchUserID)
	mu.Lock()
	defer mu.Unlock()

	rd := &Redemption{
		ID:           ev.ID,
		TwitchUserID: rw.TwitchUserID,
		RewardID:     rw.TwitchRewardID,
		ViewerID:     ev.UserID,
		ViewerLogin:  ev.UserLogin,
		ViewerName:   ev.UserName,
		UserInput:    ev.UserInput,
		Amount:       rw.BonusAmount,
		Status:       StatusReceived,
	}
	created, err := s.repo.CreateRedemption(ctx, rd)
	if err != nil {
		s.log.Error("create redemption", "id", ev.ID, "err", err)
		return
	}
	if !created {
		existing, err := s.repo.GetRedemption(ctx, ev.ID)
		if err != nil {
			s.log.Error("load redemption", "id", ev.ID, "err", err)
			return
		}
		switch existing.Status {
		case StatusFulfilled:
			// Заявка осталась в очереди Twitch: дозакрываем.
			s.finalizeTwitch(ctx, rw, ev.ID, twitch.StatusFulfilled)
			return
		case StatusRefunded:
			s.finalizeTwitch(ctx, rw, ev.ID, twitch.StatusCanceled)
			return
		case StatusReceived:
			rd = existing // решение не было принято (например, сервис упал): обрабатываем заново
		default:
			return // giving / needs_review: автоматически не трогаем
		}
	}
	s.process(ctx, rw, rd)
}

func (s *Service) process(ctx context.Context, rw *Reward, rd *Redemption) {
	uid := rw.TwitchUserID

	acc, err := s.repo.GetAccount(ctx, uid)
	if err != nil || acc.Invalid {
		s.refundPublic(ctx, rw, rd, "У стримера отключена интеграция с MemeAlerts", "у стримера отключена интеграция с MemeAlerts")
		return
	}

	nicks := candidates(rw, rd)
	if len(nicks) == 0 {
		s.refundPublic(ctx, rw, rd, "Не указан ник MemeAlerts", "не указан ник MemeAlerts")
		return
	}

	var (
		sup   Supporter
		res   = matchNone
		dupes []Supporter
		nick  string // ник, по которому нашли (или первый, если не нашли)
	)
	for i, n := range nicks {
		err := s.withRetry(ctx, func() error {
			var e error
			sup, res, dupes, e = s.lookup(ctx, uid, n)
			return e
		})
		if err != nil {
			s.log.Warn("supporter lookup failed", "redemption", rd.ID, "err", err)
			if errors.Is(err, ErrMemeAlertsReauth) {
				s.refundPublic(ctx, rw, rd, "У стримера отключена интеграция с MemeAlerts", "у стримера отключена интеграция с MemeAlerts")
			} else {
				s.refundPublic(ctx, rw, rd, "MemeAlerts временно недоступен, попробуйте позже", "MemeAlerts временно недоступен, попробуйте позже")
			}
			return
		}
		nick = n
		if res != matchNone || i == len(nicks)-1 {
			break
		}
	}
	if res == matchNone {
		nick = nicks[0]
	}

	switch res {
	case matchNone:
		msg := fmt.Sprintf("Ник «%s» не найден среди саппортеров стримера", nick)
		s.refundPublic(ctx, rw, rd, msg, fmt.Sprintf("ник «%s» не найден среди саппортеров", nick))
		return
	case matchAmbiguous:
		s.refundPublic(ctx, rw, rd, ambiguousReason(nick, dupes, rw.UseUserInput),
			fmt.Sprintf("ник «%s» принадлежит нескольким пользователям: укажите ссылку memealerts.com/... или ID вместо ника", nick))
		return
	case matchMuted:
		s.refundPublic(ctx, rw, rd, "Стример заблокировал этого пользователя в MemeAlerts", "начисление для этого пользователя недоступно")
		return
	}

	// Дальше действие необратимо: фиксируем намерение ДО вызова API.
	err = s.repo.UpdateRedemption(ctx, rd.ID, givingFields(sup))
	if err != nil {
		// Без записи в журнал не рискуем выдавать бонус.
		s.log.Error("persist giving state", "redemption", rd.ID, "err", err)
		s.refundPublic(ctx, rw, rd, "Внутренняя ошибка, попробуйте позже", "внутренняя ошибка, попробуйте позже")
		return
	}

	err = s.callMA(ctx, uid, func(access string) error {
		return s.ma.GiveBonus(ctx, access, sup.SupporterID, rw.BonusAmount)
	})
	switch {
	case err == nil:
		s.setStatus(ctx, rd.ID, StatusFulfilled, "")
		s.log.Info("bonus given", "redemption", rd.ID, "supporter", sup.SupporterID, "amount", rw.BonusAmount)
		s.finalizeTwitch(ctx, rw, rd.ID, twitch.StatusFulfilled)
		s.notifyChat(rw, rd, sup.SupporterName, true, "")
	case definitelyNotApplied(err):
		s.log.Warn("bonus rejected", "redemption", rd.ID, "err", err)
		s.refundPublic(ctx, rw, rd, "MemeAlerts отклонил начисление: "+apiMessage(err), "MemeAlerts отклонил начисление")
	default:
		// Запрос мог дойти: не повторяем и не возвращаем баллы, чтобы не выдать бонус дважды.
		s.log.Error("bonus outcome unknown", "redemption", rd.ID, "err", err)
		s.setStatus(ctx, rd.ID, StatusNeedsReview, "Не удалось подтвердить начисление: проверьте вручную. Заявка осталась в очереди Twitch.")
	}
}

// givingFields: поля журнала на момент перехода в giving. Имена колонок GORM строит из имён полей
// Redemption (MemeAlertsUserID → meme_alerts_user_id); тест проверяет, что они совпадают с моделью.
func givingFields(sup Supporter) map[string]any {
	return map[string]any{
		"status":              StatusGiving,
		"matched_nick":        sup.SupporterName,
		"meme_alerts_user_id": sup.SupporterID,
	}
}

// refund возвращает баллы зрителю (CANCELED) и записывает причину.
func (s *Service) refund(ctx context.Context, rw *Reward, rd *Redemption, reason string) {
	s.refundPublic(ctx, rw, rd, reason, reason)
}

// refundPublic: reason видит только стример (журнал в dashboard), public можно писать в чат.
func (s *Service) refundPublic(ctx context.Context, rw *Reward, rd *Redemption, reason, public string) {
	s.setStatus(ctx, rd.ID, StatusRefunded, reason)
	s.finalizeTwitch(ctx, rw, rd.ID, twitch.StatusCanceled)
	s.notifyChat(rw, rd, "", false, public)
}

func (s *Service) setStatus(ctx context.Context, id string, st RedemptionStatus, reason string) {
	err := s.repo.UpdateRedemption(ctx, id, map[string]any{"status": st, "reason": reason})
	if err != nil {
		s.log.Error("update redemption", "id", id, "status", st, "err", err)
	}
}

// finalizeTwitch выставляет статус заявки на Twitch с несколькими попытками.
// Если не вышло, заявка остаётся UNFULFILLED, и повторная попытка произойдёт при следующей догонке.
func (s *Service) finalizeTwitch(ctx context.Context, rw *Reward, redemptionID, status string) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var creds twitch.Credentials
		creds, err = s.tokens.Credentials(ctx, rw.TwitchUserID)
		if err == nil {
			err = s.helix.UpdateRedemptionStatus(ctx, creds, rw.TwitchUserID, rw.TwitchRewardID, redemptionID, status)
		}
		if err == nil {
			return
		}
		if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusBadRequest {
			// Уже не UNFULFILLED (закрыта вручную): повторять бессмысленно.
			s.log.Info("redemption already closed on twitch", "id", redemptionID)
			return
		}
		if errors.Is(err, twitch.ErrReauthRequired) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	s.log.Error("set twitch redemption status", "id", redemptionID, "status", status, "err", err)
}

// CatchUp обрабатывает заявки, которые остались в очереди Twitch (UNFULFILLED),
// например пришедшие, пока сервис был выключен или переподключался.
func (s *Service) CatchUp(ctx context.Context, userID string) {
	rw, err := s.repo.GetReward(ctx, userID)
	if err != nil || rw.TwitchRewardID == "" || !rw.Enabled {
		return
	}
	creds, err := s.tokens.Credentials(ctx, userID)
	if err != nil {
		s.log.Warn("catch-up: credentials", "user", userID, "err", err)
		return
	}
	items, err := s.helix.ListRedemptions(ctx, creds, userID, rw.TwitchRewardID, "UNFULFILLED")
	if err != nil {
		s.log.Warn("catch-up: list redemptions", "user", userID, "err", err)
		return
	}
	for _, it := range items {
		s.HandleRedemption(ctx, it)
	}
}

// ---------------------------------------------------------------- matching

func normalizeNick(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	if i := strings.LastIndex(s, "memealerts.com/"); i >= 0 {
		s = s[i+len("memealerts.com/"):]
	}
	return strings.Trim(s, "/ ")
}

// candidates: какие ники пробуем искать среди саппортеров.
// Если награда просит ввод, берём только введённый ник; иначе имя и логин Twitch.
func candidates(rw *Reward, rd *Redemption) []string {
	var raw []string
	if rw.UseUserInput {
		raw = []string{rd.UserInput}
	} else {
		raw = []string{rd.ViewerName, rd.ViewerLogin}
	}
	var out []string
	for _, r := range raw {
		n := normalizeNick(r)
		if n == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if strings.EqualFold(o, n) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

var memeAlertsIDRe = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)

// isSupporterID: похоже ли значение на id пользователя MemeAlerts (24 hex-символа).
func isSupporterID(s string) bool { return memeAlertsIDRe.MatchString(s) }

func sameNick(sp Supporter, nick string) bool {
	if strings.EqualFold(strings.TrimSpace(sp.SupporterName), nick) {
		return true
	}
	return sp.SupporterLink != "" && strings.EqualFold(sp.SupporterLink, nick)
}

// matchesInput: совпадение по нику, ссылке или (для значения вида id) по id пользователя.
func matchesInput(sp Supporter, nick string) bool {
	if sameNick(sp, nick) {
		return true
	}
	return isSupporterID(nick) && strings.EqualFold(sp.SupporterID, nick)
}

// pickSupporter выбирает единственного подходящего саппортера. Если ввод в точности равен id,
// побеждает владелец этого id: id уникален, так что дубликатов имени он не боится.
// Второй результат: найденные кандидаты при неоднозначности (иначе nil).
func pickSupporter(found []Supporter, nick string) (Supporter, matchResult, []Supporter) {
	byID := map[string]Supporter{}
	var order []string
	for _, sp := range found {
		if _, ok := byID[sp.SupporterID]; !ok {
			order = append(order, sp.SupporterID)
		}
		byID[sp.SupporterID] = sp
	}
	if isSupporterID(nick) {
		for _, id := range order {
			if strings.EqualFold(id, nick) {
				sp := byID[id]
				if sp.MutedByStreamer {
					return sp, matchMuted, nil
				}
				return sp, matchFound, nil
			}
		}
	}
	switch len(order) {
	case 0:
		return Supporter{}, matchNone, nil
	case 1:
		sp := byID[order[0]]
		if sp.MutedByStreamer {
			return sp, matchMuted, nil
		}
		return sp, matchFound, nil
	}
	dupes := make([]Supporter, 0, len(order))
	for _, id := range order {
		dupes = append(dupes, byID[id])
	}
	return Supporter{}, matchAmbiguous, dupes
}

// ambiguousReason: причина для стримера (dashboard) с перечнем дубликатов.
func ambiguousReason(nick string, dupes []Supporter, userInput bool) string {
	const maxShown = 5
	var b strings.Builder
	fmt.Fprintf(&b, "Ник «%s» подходит нескольким саппортерам (%d): ", nick, len(dupes))
	for i, d := range dupes {
		if i == maxShown {
			fmt.Fprintf(&b, " и ещё %d", len(dupes)-maxShown)
			break
		}
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s (ID %s", d.SupporterName, d.SupporterID)
		if d.SupporterLink != "" {
			fmt.Fprintf(&b, ", memealerts.com/%s", d.SupporterLink)
		}
		b.WriteString(")")
	}
	if userInput {
		b.WriteString(". Зритель может указать вместо ника свой ID или ссылку memealerts.com/…")
	} else {
		b.WriteString(". Включите у награды ввод ника: тогда зритель сможет указать свой ID или ссылку memealerts.com/…")
	}
	return b.String()
}

// lookup ищет точное совпадение среди саппортеров стримера (поиск API по имени + точная фильтрация).
// Если ввод похож на id и по имени ничего нет, просматривает список саппортеров целиком:
// поиск API работает по имени, id в нём не ищется.
func (s *Service) lookup(ctx context.Context, userID, nick string) (Supporter, matchResult, []Supporter, error) {
	found, err := s.scanSupporters(ctx, userID, nick, nick)
	if err != nil {
		return Supporter{}, matchNone, nil, err
	}
	if len(found) == 0 && isSupporterID(nick) {
		found, err = s.scanSupporters(ctx, userID, "", nick)
		if err != nil {
			return Supporter{}, matchNone, nil, err
		}
	}
	sp, res, dupes := pickSupporter(found, nick)
	return sp, res, dupes, nil
}

// scanSupporters листает саппортеров (query пустой = все) и оставляет подходящих под ввод.
func (s *Service) scanSupporters(ctx context.Context, userID, query, nick string) ([]Supporter, error) {
	var found []Supporter
	for page := 0; page < supporterMaxPages; page++ {
		skip := page * supporterPageSize
		var resp *SupportersPage
		err := s.callMA(ctx, userID, func(access string) error {
			var e error
			resp, e = s.ma.Supporters(ctx, access, query, skip, supporterPageSize)
			return e
		})
		if err != nil {
			return nil, err
		}
		for _, sp := range resp.Data {
			if matchesInput(sp, nick) {
				found = append(found, sp)
			}
		}
		if len(resp.Data) < supporterPageSize || skip+supporterPageSize >= resp.Total {
			break
		}
	}
	return found, nil
}

// ---------------------------------------------------------------- error classification

// withRetry повторяет только безопасные (чтение) операции при временных сбоях.
func (s *Service) withRetry(ctx context.Context, fn func() error) error {
	delays := []time.Duration{time.Second, 4 * time.Second}
	for i := 0; ; i++ {
		err := fn()
		if err == nil || !retryable(err) || i >= len(delays) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delays[i]):
		}
	}
}

func retryable(err error) bool {
	if errors.Is(err, ErrMemeAlertsReauth) || errors.Is(err, ErrNotConnected) || errors.Is(err, context.Canceled) {
		return false
	}
	if se, ok := httpx.AsStatus(err); ok {
		return se.Code >= 500 || se.Code == http.StatusTooManyRequests || se.Code == http.StatusRequestTimeout
	}
	return true // сетевая ошибка
}

// definitelyNotApplied: true, если по ошибке точно ясно, что бонус не начислен.
// Любой 4xx (кроме 408) означает, что сервер отклонил запрос. Сетевые ошибки и 5xx неоднозначны.
func definitelyNotApplied(err error) bool {
	if errors.Is(err, ErrMemeAlertsReauth) || errors.Is(err, ErrNotConnected) || errors.Is(err, ErrBonusRejected) {
		return true
	}
	if se, ok := httpx.AsStatus(err); ok {
		return se.Code >= 400 && se.Code < 500 && se.Code != http.StatusRequestTimeout
	}
	return false
}

// apiMessage достаёт короткое пояснение из ошибки API для журнала.
func apiMessage(err error) string {
	se, ok := httpx.AsStatus(err)
	if !ok {
		return err.Error()
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(se.Body), &body) == nil && body.Message != "" {
		return body.Message
	}
	return fmt.Sprintf("HTTP %d", se.Code)
}
