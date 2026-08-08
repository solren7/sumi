package handlers

import (
	"strconv"

	"sumi/internal/services"
	"sumi/middleware"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
)

// ListCategories godoc
// @Summary List system categories
// @Tags Categories
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param type query int false "Category type: 1 expense, 2 income" default(1)
// @Success 200 {array} domain.CategoryNode
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/categories [get]
func (h *Handler) ListCategories(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	rawType := c.Query("type", "1")
	categoryType, err := strconv.ParseInt(rawType, 10, 16)
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	items, err := h.S.Category.ListCategoriesByUser(c.Context(), userID, int16(categoryType))
	if err != nil {
		return err
	}

	return c.JSON(items)
}

type CreateCategoryRequest struct {
	Name       string `json:"name"`
	Type       int16  `json:"type"`
	ParentID   int64  `json:"parent_id"`
	ParentName string `json:"parent_name"`
}

// CreateCategory godoc
// @Summary Create a second-level category
// @Tags Categories
// @Accept json
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param request body CreateCategoryRequest true "Category payload"
// @Success 201 {object} domain.CategoryNode
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Router /api/categories [post]
func (h *Handler) CreateCategory(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	req := new(CreateCategoryRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	category, err := h.S.Category.CreateCategory(c.Context(), userID, services.CreateCategoryInput{
		Name:       req.Name,
		Type:       req.Type,
		ParentID:   req.ParentID,
		ParentName: req.ParentName,
	})
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(category)
}
