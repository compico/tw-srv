// Package config читает настройки из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env накапливает ошибки, чтобы показать все проблемы конфигурации за один запуск.
type Env struct {
	errs []error
}

func (e *Env) String(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (e *Env) Require(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		e.errs = append(e.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (e *Env) Bool(key string, def bool) bool {
	v := e.String(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return b
}

func (e *Env) Duration(key string, def time.Duration) time.Duration {
	v := e.String(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return d
}

// List разбивает значение по запятым и пробелам.
func (e *Env) List(key string, def []string) []string {
	v := e.String(key, "")
	if v == "" {
		return def
	}
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
}

func (e *Env) Err() error {
	return errors.Join(e.errs...)
}
