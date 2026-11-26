// Package chatbot: сообщения в чат стримера после обмена баллов (успех или возврат).
// Использует отдельное Twitch-приложение и отдельный токен со scope user:write:chat,
// поэтому основная авторизация (награды и баллы) не затрагивается.
package chatbot

import "time"

// Scope токена бота: писать в чат.
const Scope = "user:write:chat"

// Bot: подключённый аккаунт, от имени которого пишем в чат стримера.
type Bot struct {
	TwitchUserID    string `gorm:"primaryKey;size:32"` // стример, чей чат
	BotUserID       string `gorm:"size:32"`
	BotLogin        string `gorm:"size:64"`
	AccessTokenEnc  string
	RefreshTokenEnc string
	ExpiresAt       time.Time
	Invalid         bool // Twitch отозвал доступ, нужно подключить заново
	LastError       string
	LastErrorAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ConnectState: одноразовая ссылка подключения бота. Хранится на сервере, а не в cookie браузера:
// так подключить бота можно из другого браузера (например, приватного окна с другим аккаунтом).
type ConnectState struct {
	State        string    `gorm:"primaryKey;size:64"`
	TwitchUserID string    `gorm:"size:32"`
	ExpiresAt    time.Time `gorm:"index"`
}

// Settings: шаблоны сообщений. По умолчанию всё выключено.
type Settings struct {
	TwitchUserID   string `gorm:"primaryKey;size:32"`
	SuccessEnabled bool
	SuccessText    string
	FailEnabled    bool
	FailText       string
	UpdatedAt      time.Time
}

const (
	DefaultSuccessText = "@{user}, мемкоины ({amount}) начислены на твой аккаунт MemeAlerts!"
	DefaultFailText    = "@{user}, мемкоины не начислены: {reason}. Баллы возвращены."

	MaxTemplateRunes = 400
	MaxMessageRunes  = 500 // лимит Twitch
)
