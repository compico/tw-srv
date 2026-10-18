// Package authclient: gRPC-клиент к сервису twitch-auth.
// Реализует twitch.TokenSource и twitch.SessionValidator.
package authclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/compico/tw-srv/internal/authpb"
	"github.com/compico/tw-srv/internal/twitch"
)

const (
	callTimeout  = 10 * time.Second
	sessionCache = 30 * time.Second
)

type Client struct {
	conn *grpc.ClientConn
	api  authpb.AuthServiceClient

	mu       sync.Mutex
	sessions map[string]cachedIdentity
}

type cachedIdentity struct {
	id  twitch.Identity
	exp time.Time
}

// Dial создаёт клиент. Соединение устанавливается лениво, поэтому порядок старта контейнеров неважен.
// Трафик идёт внутри docker-сети без TLS; защита: общий секрет в metadata.
func Dial(addr, secret string) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(bearer(secret)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial auth service: %w", err)
	}
	return &Client{conn: conn, api: authpb.NewAuthServiceClient(conn), sessions: map[string]cachedIdentity{}}, nil
}

func bearer(secret string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+secret)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func (c *Client) Close() error { return c.conn.Close() }

// ValidateSession проверяет cookie-сессию. Успешные ответы кэшируются на 30 секунд,
// чтобы каждый запрос фронтенда не ходил по gRPC.
func (c *Client) ValidateSession(ctx context.Context, sessionID string) (twitch.Identity, error) {
	if sessionID == "" {
		return twitch.Identity{}, twitch.ErrNoSession
	}
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.sessions[sessionID]; ok && now.Before(e.exp) {
		c.mu.Unlock()
		return e.id, nil
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := c.api.ValidateSession(ctx, &authpb.ValidateSessionRequest{SessionId: sessionID})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			c.mu.Lock()
			delete(c.sessions, sessionID)
			c.mu.Unlock()
			return twitch.Identity{}, twitch.ErrNoSession
		}
		return twitch.Identity{}, fmt.Errorf("validate session: %w", err)
	}
	id := twitch.Identity{
		UserID:      resp.GetTwitchUserId(),
		Login:       resp.GetLogin(),
		DisplayName: resp.GetDisplayName(),
		AvatarURL:   resp.GetAvatarUrl(),
	}

	c.mu.Lock()
	if len(c.sessions) > 1000 {
		for k, e := range c.sessions {
			if now.After(e.exp) {
				delete(c.sessions, k)
			}
		}
	}
	c.sessions[sessionID] = cachedIdentity{id: id, exp: now.Add(sessionCache)}
	c.mu.Unlock()
	return id, nil
}

// Credentials возвращает свежий токен Twitch пользователя.
func (c *Client) Credentials(ctx context.Context, userID string) (twitch.Credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := c.api.GetAccessToken(ctx, &authpb.GetAccessTokenRequest{TwitchUserId: userID})
	if err != nil {
		switch status.Code(err) {
		case codes.FailedPrecondition:
			return twitch.Credentials{}, twitch.ErrReauthRequired
		case codes.NotFound:
			return twitch.Credentials{}, twitch.ErrUnknownUser
		}
		return twitch.Credentials{}, fmt.Errorf("get access token: %w", err)
	}
	return twitch.Credentials{AccessToken: resp.GetAccessToken(), ClientID: resp.GetClientId()}, nil
}
