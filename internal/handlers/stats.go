package handlers

import (
	"strconv"

	"sumi/middleware"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
)

// GetMonthlyStats godoc
// @Summary Get monthly stats
// @Tags Stats
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param month query string false "Month in YYYY-MM"
// @Success 200 {object} domain.MonthlyStatsOutput
// @Failure 401 {object} ErrorResponse
// @Router /api/stats/monthly [get]
// @Router /api/stats/home [get]
func (h *Handler) GetMonthlyStats(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	stats, err := h.S.Stats.GetMonthlyStats(c.Context(), userID, c.Query("month"))
	if err != nil {
		return err
	}
	return c.JSON(stats)
}

// GetDailyStats godoc
// @Summary Get daily stats
// @Tags Stats
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param month query string false "Month in YYYY-MM"
// @Success 200 {object} domain.DailyStatsOutput
// @Failure 401 {object} ErrorResponse
// @Router /api/stats/daily [get]
func (h *Handler) GetDailyStats(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	stats, err := h.S.Stats.GetDailyStats(c.Context(), userID, c.Query("month"))
	if err != nil {
		return err
	}
	return c.JSON(stats)
}

// GetCategoryStats godoc
// @Summary Get category stats
// @Tags Stats
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param month query string false "Month in YYYY-MM"
// @Param type query int false "Transaction type: 1 expense, 2 income" default(1)
// @Success 200 {object} domain.CategoryStatsOutput
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/stats/category [get]
func (h *Handler) GetCategoryStats(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	rawType := c.Query("type", "1")
	parsedType, err := strconv.ParseInt(rawType, 10, 16)
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	stats, err := h.S.Stats.GetCategoryStats(c.Context(), userID, c.Query("month"), int16(parsedType))
	if err != nil {
		return err
	}
	return c.JSON(stats)
}
