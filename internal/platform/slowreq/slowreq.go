// Package slowreq: middleware, который пишет в лог запросы, выполнявшиеся дольше порога.
// Нужен для поиска долгих запросов и отладки.
package slowreq

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

// DefaultThreshold: запросы дольше этого времени считаются долгими.
const DefaultThreshold = 15 * time.Second

// New запускает следующий обработчик цепочки; после его завершения (в defer) пишет предупреждение,
// если запрос шёл дольше threshold. В лог попадает только путь без query: в адресах возврата
// OAuth бывают code и state.
func New(log *slog.Logger, threshold time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		defer func() {
			if elapsed := time.Since(start); elapsed > threshold {
				log.Warn("slow request",
					"method", c.Method(),
					"path", c.Path(),
					"status", c.Response().StatusCode(),
					"duration", elapsed.Round(time.Millisecond).String(),
					"threshold", threshold.String(),
				)
			}
		}()
		return c.Next()
	}
}
