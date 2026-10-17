// Package auth: домен авторизации через Twitch: вход пользователя, сессии,
// хранение и обновление токенов. Больше ничего о бизнес-логике не знает.
package auth

import "time"

type User struct {
	ID          string `gorm:"primaryKey;size:32"`
	Login       string `gorm:"size:64;index"`
	DisplayName string `gorm:"size:64"`
	AvatarURL   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Token хранит пару токенов Twitch в зашифрованном виде.
type Token struct {
	UserID          string `gorm:"primaryKey;size:32"`
	AccessTokenEnc  string
	RefreshTokenEnc string
	Scopes          string
	ExpiresAt       time.Time `gorm:"index"`
	LastValidatedAt time.Time `gorm:"index"`
	Invalid         bool      `gorm:"index"`
	InvalidReason   string
	UpdatedAt       time.Time
}

// Session: cookie-сессия. В базе лежит только sha256 идентификатора.
type Session struct {
	IDHash    string    `gorm:"column:id_hash;primaryKey;size:64"`
	UserID    string    `gorm:"size:32;index"`
	ExpiresAt time.Time `gorm:"index"`
	CreatedAt time.Time
}

// LoginState защищает OAuth-вход от CSRF и хранит, куда вернуть пользователя.
type LoginState struct {
	State     string `gorm:"column:state;primaryKey;size:64"`
	ReturnTo  string
	ExpiresAt time.Time `gorm:"index"`
}
