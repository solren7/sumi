package middleware

import (
	"strings"

	"sumi/internal/services"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	localUserID   = "user_id"
	localEmail    = "email"
	localAuthType = "auth_type"
	localScopes   = "scopes"
)

func JWTOnly(auth *services.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		tokenString, err := extractBearerToken(c.Get("Authorization"))
		if err != nil {
			return err
		}

		claims, err := auth.ParseAccessToken(tokenString)
		if err != nil {
			return err
		}

		userID, err := uuid.Parse(claims.UserID)
		if err != nil {
			return errorx.ErrUnauthorized
		}

		c.Locals(localUserID, userID)
		c.Locals(localEmail, claims.Email)
		c.Locals(localAuthType, "jwt")
		return c.Next()
	}
}

func JWTOrAPIKey(auth *services.AuthService, apiKeys *services.APIKeyService, requiredScopes ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if authHeader := c.Get("Authorization"); authHeader != "" {
			tokenString, err := extractBearerToken(authHeader)
			if err != nil {
				return err
			}
			claims, err := auth.ParseAccessToken(tokenString)
			if err != nil {
				return err
			}
			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				return errorx.ErrUnauthorized
			}

			c.Locals(localUserID, userID)
			c.Locals(localEmail, claims.Email)
			c.Locals(localAuthType, "jwt")
			return c.Next()
		}

		apiKey := strings.TrimSpace(c.Get("X-API-Key"))
		if apiKey == "" {
			return errorx.ErrUnauthorized
		}

		identity, err := apiKeys.Validate(c.Context(), apiKey, requiredScopes)
		if err != nil {
			return err
		}

		c.Locals(localUserID, identity.UserID)
		c.Locals(localAuthType, "api_key")
		c.Locals(localScopes, identity.Scopes)
		return c.Next()
	}
}

func UserID(c fiber.Ctx) (uuid.UUID, error) {
	value := c.Locals(localUserID)
	userID, ok := value.(uuid.UUID)
	if !ok {
		return uuid.Nil, errorx.ErrUnauthorized
	}
	return userID, nil
}

func extractBearerToken(authHeader string) (string, error) {
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return "", errorx.ErrUnauthorized
	}
	return parts[1], nil
}
