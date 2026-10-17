// Package httpx содержит минимальный помощник для исходящих JSON-запросов.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// StatusError возвращается, когда сервер ответил не 2xx.
// Отличие от сетевой ошибки важно: по нему видно, что запрос точно дошёл до сервера.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return fmt.Sprintf("http %d: %s", e.Code, body)
}

// AsStatus достаёт *StatusError из цепочки ошибок.
func AsStatus(err error) (*StatusError, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// DoJSON выполняет запрос и декодирует JSON-ответ в out (если out != nil).
func DoJSON(c *http.Client, req *http.Request, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Code: resp.StatusCode, Body: string(body)}
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
