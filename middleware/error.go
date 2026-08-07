package middleware

import (
	"errors"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
)

func ErrorHandler(ctx fiber.Ctx, err error) error {
	// Default to 500
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	var e *errorx.Error
	if errors.As(err, &e) {
		code = e.Code
		message = e.Message
	} else {
		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			code = fiberErr.Code
			message = fiberErr.Message
		}
	}
	return ctx.Status(code).JSON(fiber.Map{
		"success": false,
		"code":    code,
		"message": message,
	})
}
