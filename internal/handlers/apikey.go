package handlers

import (
	"time"

	"sumi/internal/repository/dbgen"
	"sumi/internal/services"
	"sumi/middleware"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type CreateAPIKeyRequest struct {
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt *string   `json:"expires_at"`
}

type APIKeyResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	Status     string   `json:"status"`
	LastUsedAt *string  `json:"last_used_at,omitempty"`
	ExpiresAt  *string  `json:"expires_at,omitempty"`
	CreatedAt  string   `json:"created_at"`
}

type CreatedAPIKeyResponse struct {
	APIKeyResponse
	Key string `json:"key"`
}

// CreateAPIKey godoc
// @Summary Create API key
// @Tags API Keys
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateAPIKeyRequest true "API key payload"
// @Success 201 {object} CreatedAPIKeyResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/api-keys [post]
func (h *Handler) CreateAPIKey(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	req := new(CreateAPIKeyRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			return errorx.New(400, "expires_at must be RFC3339")
		}
		expiresAt = &parsed
	}

	result, err := h.S.APIKey.Create(c.Context(), userID, services.CreateAPIKeyInput{
		Name:      req.Name,
		Scopes:    req.Scopes,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(CreatedAPIKeyResponse{
		APIKeyResponse: toAPIKeyResponse(*result.Record),
		Key:            result.Key,
	})
}

// ListAPIKeys godoc
// @Summary List API keys
// @Tags API Keys
// @Produce json
// @Security BearerAuth
// @Success 200 {array} APIKeyResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/api-keys [get]
func (h *Handler) ListAPIKeys(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	items, err := h.S.APIKey.List(c.Context(), userID)
	if err != nil {
		return err
	}

	response := make([]APIKeyResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toAPIKeyResponse(item))
	}
	return c.JSON(response)
}

// RevokeAPIKey godoc
// @Summary Revoke API key
// @Tags API Keys
// @Produce json
// @Security BearerAuth
// @Param id path string true "API key UUID"
// @Success 204
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/api-keys/{id}/revoke [post]
func (h *Handler) RevokeAPIKey(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	apiKeyID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	if err := h.S.APIKey.Revoke(c.Context(), userID, apiKeyID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// DeleteAPIKey godoc
// @Summary Delete API key
// @Tags API Keys
// @Produce json
// @Security BearerAuth
// @Param id path string true "API key UUID"
// @Success 204
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/api-keys/{id} [delete]
func (h *Handler) DeleteAPIKey(c fiber.Ctx) error {
	return h.RevokeAPIKey(c)
}

func toAPIKeyResponse(item dbgen.ApiKey) APIKeyResponse {
	var lastUsedAt *string
	if item.LastUsedAt.Valid {
		value := item.LastUsedAt.Time.Format(time.RFC3339)
		lastUsedAt = &value
	}
	var expiresAt *string
	if item.ExpiresAt.Valid {
		value := item.ExpiresAt.Time.Format(time.RFC3339)
		expiresAt = &value
	}

	return APIKeyResponse{
		ID:         item.ID.String(),
		Name:       item.Name,
		Prefix:     item.KeyPrefix,
		Scopes:     item.Scopes,
		Status:     item.Status,
		LastUsedAt: lastUsedAt,
		ExpiresAt:  expiresAt,
		CreatedAt:  item.CreatedAt.Format(time.RFC3339),
	}
}
