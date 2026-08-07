package middleware

import (
	"sumi/pkg/logx"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func LogxMeta(c fiber.Ctx) error {
	rid := c.Get("X-Request-ID")
	if rid == "" {
		rid = uuid.New().String()
	}

	// Store Metadata into context; the metadataHook extracts it automatically.
	ctx := logx.NewContext(c.Context(), logx.Metadata{
		RequestID: rid,
	})
	c.SetContext(ctx)

	c.Set("X-Request-ID", rid)
	return c.Next()
}
