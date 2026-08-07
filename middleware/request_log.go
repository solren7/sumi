package middleware

import (
	"time"

	"sumi/pkg/logx"

	"github.com/gofiber/fiber/v3"
)

func RequestLog() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		latency := time.Since(start)

		fields := map[string]any{
			"method":     c.Method(),
			"path":       c.Path(),
			"status":     c.Response().StatusCode(),
			"latency_ms": latency.Milliseconds(),
			"ip":         c.IP(),
		}

		if ua := c.Get("User-Agent"); ua != "" {
			fields["user_agent"] = ua
		}

		entry := logx.WithCtx(c.Context()).WithFields(fields)
		if err != nil {
			entry.WithError(err).Error("request completed with error")
			return err
		}

		entry.Info("request completed")
		return nil
	}
}
