package memealert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/compico/overlaycomposer/internal/platform/httpx"
)

const (
	apiBase  = "https://memealerts.com"
	MaxBonus = 1000 // лимит API на одну выдачу
)

var (
	// ErrInvalidGrant: refresh token или code недействителен, нужна повторная авторизация.
	ErrInvalidGrant = errors.New("memealerts: invalid grant")
	// ErrBonusRejected: API ответил 2xx, но result != true. Бонус точно не выдан.
	ErrBonusRejected = errors.New("memealerts: bonus rejected")
)

// APIClient: клиент публичного API MemeAlerts (ext-api).
type APIClient struct {
	base         string
	clientID     string
	clientSecret string
	http         *http.Client
}

func NewAPIClient(clientID, clientSecret string) *APIClient {
	return &APIClient{
		base:         apiBase,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type Profile struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Channel struct {
		UniqueLink string `json:"unique_link"`
	} `json:"channel"`
}

type Supporter struct {
	SupporterID     string `json:"supporterId"`
	SupporterName   string `json:"supporterName"`
	SupporterLink   string `json:"supporterLink"`
	MutedByStreamer bool   `json:"mutedByStreamer"`
}

type SupportersPage struct {
	Data  []Supporter `json:"data"`
	Total int         `json:"total"`
}

func (c *APIClient) AuthorizeURL(redirectURI, state string, scopes []string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	return c.base + "/oauth/authorize?" + q.Encode()
}

func (c *APIClient) ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenPair, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("redirect_uri", redirectURI)
	form.Set("code", code)
	return c.token(ctx, form)
}

// Refresh: refresh token одноразовый, каждый успешный ответ содержит новый,
// и его нужно сохранить до дальнейших вызовов.
func (c *APIClient) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	return c.token(ctx, form)
}

func (c *APIClient) token(ctx context.Context, form url.Values) (*TokenPair, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var out TokenPair
	if err := httpx.DoJSON(c.http, req, &out); err != nil {
		if se, ok := httpx.AsStatus(err); ok {
			var body struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(se.Body), &body) == nil && body.Error == "invalid_grant" {
				return nil, fmt.Errorf("%w: %s", ErrInvalidGrant, se.Body)
			}
		}
		return nil, err
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		return nil, errors.New("memealerts: token response without tokens")
	}
	return &out, nil
}

func (c *APIClient) do(ctx context.Context, access, method, path string, q url.Values, body, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var buf *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(b)
	}
	var req *http.Request
	var err error
	if buf != nil {
		req, err = http.NewRequestWithContext(ctx, method, u, buf)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return httpx.DoJSON(c.http, req, out)
}

func (c *APIClient) Profile(ctx context.Context, access string) (*Profile, error) {
	var out struct {
		Data Profile `json:"data"`
	}
	if err := c.do(ctx, access, http.MethodGet, "/api/v1/user/oauth", nil, nil, &out); err != nil {
		return nil, err
	}
	if out.Data.ID == "" {
		return nil, errors.New("memealerts: empty profile")
	}
	return &out.Data, nil
}

// Supporters ищет саппортеров стримера по имени. query может быть пустым.
func (c *APIClient) Supporters(ctx context.Context, access, query string, skip, limit int) (*SupportersPage, error) {
	q := url.Values{}
	q.Set("skip", strconv.Itoa(skip))
	q.Set("limit", strconv.Itoa(limit))
	if query != "" {
		q.Set("query", query)
	}
	var out SupportersPage
	if err := c.do(ctx, access, http.MethodGet, "/api/v1/user/supporters", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GiveBonus начисляет мемкоины зрителю. Метод не идемпотентен: повторять вслепую нельзя.
func (c *APIClient) GiveBonus(ctx context.Context, access, userID string, value int) error {
	var out struct {
		Result bool `json:"result"`
	}
	body := map[string]any{"userId": userID, "value": value}
	if err := c.do(ctx, access, http.MethodPost, "/api/v1/user/give-bonus", nil, body, &out); err != nil {
		return err
	}
	if !out.Result {
		return ErrBonusRejected
	}
	return nil
}
