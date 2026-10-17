package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/compico/tw-srv/internal/platform/httpx"
)

const (
	authorizeEndpoint = "https://id.twitch.tv/oauth2/authorize"
	tokenEndpoint     = "https://id.twitch.tv/oauth2/token"
	validateEndpoint  = "https://id.twitch.tv/oauth2/validate"
	usersEndpoint     = "https://api.twitch.tv/helix/users"
)

// OAuth: клиент Authorization Code flow для Twitch.
type OAuth struct {
	clientID     string
	clientSecret string
	redirectURI  string
	http         *http.Client
}

func NewOAuth(clientID, clientSecret, redirectURI string) *OAuth {
	return &OAuth{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

func (o *OAuth) ClientID() string { return o.clientID }

type TokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scope        []string `json:"scope"`
	TokenType    string   `json:"token_type"`
}

type ValidateResponse struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	UserID    string   `json:"user_id"`
	Scopes    []string `json:"scopes"`
	ExpiresIn int      `json:"expires_in"`
}

type UserInfo struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	ProfileImageURL string `json:"profile_image_url"`
}

func (o *OAuth) AuthorizeURL(state string, scopes []string) string {
	q := url.Values{}
	q.Set("client_id", o.clientID)
	q.Set("redirect_uri", o.redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	return authorizeEndpoint + "?" + q.Encode()
}

func (o *OAuth) Exchange(ctx context.Context, code string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", o.clientID)
	form.Set("client_secret", o.clientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", o.redirectURI)
	return o.token(ctx, form)
}

// Refresh обменивает refresh token на новую пару.
// ErrInvalidGrant означает, что refresh token отозван или недействителен (нужен повторный вход).
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", o.clientID)
	form.Set("client_secret", o.clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return o.token(ctx, form)
}

func (o *OAuth) token(ctx context.Context, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var out TokenResponse
	if err := httpx.DoJSON(o.http, req, &out); err != nil {
		if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusBadRequest {
			return nil, fmt.Errorf("%w: %s", ErrInvalidGrant, se.Body)
		}
		return nil, err
	}
	return &out, nil
}

// Validate нужен по правилам Twitch: приложения с долгими сессиями проверяют токен раз в час.
func (o *OAuth) Validate(ctx context.Context, accessToken string) (*ValidateResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validateEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)

	var out ValidateResponse
	if err := httpx.DoJSON(o.http, req, &out); err != nil {
		if se, ok := httpx.AsStatus(err); ok && se.Code == http.StatusUnauthorized {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	return &out, nil
}

// CurrentUser возвращает владельца токена.
func (o *OAuth) CurrentUser(ctx context.Context, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usersEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Client-Id", o.clientID)

	var out struct {
		Data []UserInfo `json:"data"`
	}
	if err := httpx.DoJSON(o.http, req, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("twitch returned no user for token")
	}
	return &out.Data[0], nil
}
