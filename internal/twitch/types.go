// Package twitch: общее ядро для обоих сервисов: ошибки, интерфейсы,
// OAuth-клиент id.twitch.tv, Helix API и EventSub WebSocket.
package twitch

import (
	"context"
	"errors"
)

var (
	ErrNoSession      = errors.New("twitch: no valid session")
	ErrReauthRequired = errors.New("twitch: user must sign in again")
	ErrUnknownUser    = errors.New("twitch: unknown user")
	ErrInvalidGrant   = errors.New("twitch: invalid grant")
	ErrInvalidToken   = errors.New("twitch: invalid access token")
)

// Credentials: всё, что нужно для вызова Helix от имени пользователя.
type Credentials struct {
	AccessToken string
	ClientID    string
}

// Identity: кто залогинен.
type Identity struct {
	UserID      string
	Login       string
	DisplayName string
	AvatarURL   string
}

// TokenSource реализует клиент к сервису twitch-auth (gRPC).
// Возвращает ErrReauthRequired, если пользователь должен авторизоваться заново.
type TokenSource interface {
	Credentials(ctx context.Context, userID string) (Credentials, error)
}

// SessionValidator проверяет cookie-сессию через сервис twitch-auth.
type SessionValidator interface {
	ValidateSession(ctx context.Context, sessionID string) (Identity, error)
}
