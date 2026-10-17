// Package memealert: домен обмена баллов канала Twitch на мемкоины MemeAlerts:
// подключение аккаунта стримера, настройка награды, обработка использований.
package memealert

import "time"

// Account: подключённый аккаунт MemeAlerts стримера.
type Account struct {
	TwitchUserID     string `gorm:"primaryKey;size:32"`
	TwitchLogin      string `gorm:"size:64"`
	MemeAlertsUserID string `gorm:"size:64"`
	MemeAlertsName   string
	MemeAlertsLink   string
	AccessTokenEnc   string
	RefreshTokenEnc  string
	ExpiresAt        time.Time
	Invalid          bool // MemeAlerts отозвал доступ, нужно подключить заново
	TwitchReauth     bool // Twitch отозвал доступ, стримеру нужно войти заново
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Reward: настройки награды «Получить мемкоины». Одна награда на стримера.
type Reward struct {
	TwitchUserID        string `gorm:"primaryKey;size:32"`
	TwitchRewardID      string `gorm:"size:64;index"`
	Title               string
	Prompt              string
	Cost                int
	BonusAmount         int
	UseUserInput        bool // true: зритель вводит ник MemeAlerts; false: берём ник Twitch
	BackgroundColor     string
	Enabled             bool
	CooldownSeconds     int // 0 = выключено (общий кулдаун награды)
	MaxPerStream        int // 0 = выключено (всего за стрим)
	MaxPerUserPerStream int // 0 = выключено (на зрителя за стрим)
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RedemptionStatus string

const (
	StatusReceived    RedemptionStatus = "received"     // получено, решение ещё не принято
	StatusGiving      RedemptionStatus = "giving"       // запрос на выдачу бонуса отправляется
	StatusFulfilled   RedemptionStatus = "fulfilled"    // бонус выдан
	StatusRefunded    RedemptionStatus = "refunded"     // баллы возвращены зрителю
	StatusNeedsReview RedemptionStatus = "needs_review" // исход выдачи неизвестен, нужна ручная проверка
)

// Redemption: журнал использований награды. Заодно обеспечивает идемпотентность:
// одну заявку Twitch (по id) мы обрабатываем не более одного раза.
type Redemption struct {
	ID               string `gorm:"primaryKey;size:64"` // id заявки Twitch
	TwitchUserID     string `gorm:"size:32;index"`
	RewardID         string `gorm:"size:64"`
	ViewerID         string `gorm:"size:32"`
	ViewerLogin      string
	ViewerName       string
	UserInput        string
	MatchedNick      string
	MemeAlertsUserID string
	Amount           int
	Status           RedemptionStatus `gorm:"size:16;index"`
	Reason           string
	CreatedAt        time.Time `gorm:"index"`
	UpdatedAt        time.Time
}

// OAuthState: одноразовый state подключения MemeAlerts.
type OAuthState struct {
	State        string    `gorm:"column:state;primaryKey;size:64"`
	TwitchUserID string    `gorm:"size:32"`
	ExpiresAt    time.Time `gorm:"index"`
}
