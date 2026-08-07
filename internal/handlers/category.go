package handlers

import (
	"strconv"

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
// @Success 200 {array} services.CategoryNode
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
