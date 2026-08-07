package handlers

import (
	"strings"
	"time"

	"sumi/internal/services"
	"sumi/middleware"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type CheckEmailRequest struct {
	Email string `json:"email"`
}

type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Username string `json:"username,omitempty"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type UserResponse struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	Username        string `json:"username"`
	DefaultCurrency string `json:"default_currency"`
	Timezone        string `json:"timezone"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type AuthResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         UserResponse `json:"user"`
}

func toUserResponse(userID uuid.UUID, email, username, defaultCurrency, timezone string, createdAt, updatedAt time.Time) UserResponse {
	return UserResponse{
		ID:              userID.String(),
		Email:           email,
		Username:        username,
		DefaultCurrency: defaultCurrency,
		Timezone:        timezone,
		CreatedAt:       createdAt.Format(time.RFC3339),
		UpdatedAt:       updatedAt.Format(time.RFC3339),
	}
}

func sessionMeta(c fiber.Ctx) services.SessionMeta {
	ip := strings.TrimSpace(c.IP())
	userAgent := strings.TrimSpace(c.Get("User-Agent"))
	deviceID := strings.TrimSpace(c.Get("X-Device-ID"))

	meta := services.SessionMeta{}
	if ip != "" {
		meta.IPAddress = &ip
	}
	if userAgent != "" {
		meta.UserAgent = &userAgent
	}
	if deviceID != "" {
		meta.DeviceID = &deviceID
	}
	return meta
}

// CheckEmail godoc
// @Summary Check whether an email is already registered
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body CheckEmailRequest true "Email payload"
// @Success 200 {object} CheckEmailResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/auth/check-email [post]
func (h *Handler) CheckEmail(c fiber.Ctx) error {
	req := new(CheckEmailRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	exists, err := h.S.Auth.CheckEmailExists(c.Context(), req.Email)
	if err != nil {
		return err
	}

	return c.JSON(fiber.Map{"exists": exists})
}

// Register godoc
// @Summary Register a new user
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body AuthRequest true "Register payload"
// @Success 201 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/auth/register [post]
func (h *Handler) Register(c fiber.Ctx) error {
	req := new(AuthRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	output, err := h.S.Auth.Register(c.Context(), services.RegisterInput{
		Email:    req.Email,
		Password: req.Password,
		Username: req.Username,
	}, sessionMeta(c))
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(AuthResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
		User: toUserResponse(
			output.User.ID,
			output.User.Email,
			output.User.Username,
			output.User.DefaultCurrency,
			output.User.Timezone,
			output.User.CreatedAt,
			output.User.UpdatedAt,
		),
	})
}

// Login godoc
// @Summary Login with email and password
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body AuthRequest true "Login payload"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/auth/login [post]
func (h *Handler) Login(c fiber.Ctx) error {
	req := new(AuthRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	output, err := h.S.Auth.Login(c.Context(), req.Email, req.Password, sessionMeta(c))
	if err != nil {
		return err
	}

	return c.JSON(AuthResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
		User: toUserResponse(
			output.User.ID,
			output.User.Email,
			output.User.Username,
			output.User.DefaultCurrency,
			output.User.Timezone,
			output.User.CreatedAt,
			output.User.UpdatedAt,
		),
	})
}

// Refresh godoc
// @Summary Refresh access token
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body RefreshTokenRequest true "Refresh token payload"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/auth/refresh [post]
func (h *Handler) Refresh(c fiber.Ctx) error {
	req := new(RefreshTokenRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	output, err := h.S.Auth.Refresh(c.Context(), req.RefreshToken, sessionMeta(c))
	if err != nil {
		return err
	}

	return c.JSON(AuthResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
		User: toUserResponse(
			output.User.ID,
			output.User.Email,
			output.User.Username,
			output.User.DefaultCurrency,
			output.User.Timezone,
			output.User.CreatedAt,
			output.User.UpdatedAt,
		),
	})
}

// Logout godoc
// @Summary Logout current refresh token
// @Tags Auth
// @Accept json
// @Produce json
// @Param request body RefreshTokenRequest true "Refresh token payload"
// @Success 204
// @Failure 400 {object} ErrorResponse
// @Router /api/auth/logout [post]
func (h *Handler) Logout(c fiber.Ctx) error {
	req := new(RefreshTokenRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	if err := h.S.Auth.Logout(c.Context(), req.RefreshToken); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// Me godoc
// @Summary Get current user
// @Tags Auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} UserResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/auth/me [get]
func (h *Handler) Me(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	user, err := h.S.Auth.GetMe(c.Context(), userID)
	if err != nil {
		return err
	}

	return c.JSON(toUserResponse(
		user.ID,
		user.Email,
		user.Username,
		user.DefaultCurrency,
		user.Timezone,
		user.CreatedAt,
		user.UpdatedAt,
	))
}
