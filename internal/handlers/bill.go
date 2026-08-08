package handlers

import (
	"strconv"
	"strings"
	"time"

	"sumi/internal/domain"
	"sumi/internal/services"
	"sumi/middleware"
	"sumi/pkg/errorx"

	"github.com/gofiber/fiber/v3"
	"github.com/shopspring/decimal"
)

type CreateBillRequest struct {
	Type         int16  `json:"type"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	Description  string `json:"description"`
	OccurredAt   string `json:"occurred_at"`
}

type UpdateBillRequest struct {
	Type         int16  `json:"type"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	Description  string `json:"description"`
	OccurredAt   string `json:"occurred_at"`
}

type BatchCreateBillsRequest struct {
	Items []CreateBillRequest `json:"items"`
}

type BillResponse struct {
	ID          int64  `json:"id"`
	UserID      string `json:"user_id"`
	Type        int16  `json:"type"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	CategoryID  int64  `json:"category_id"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toBillResponse(b domain.Bill) BillResponse {
	return BillResponse{
		ID:          b.ID,
		UserID:      b.UserID.String(),
		Type:        b.Type,
		Amount:      b.Amount.String(),
		Currency:    b.Currency,
		CategoryID:  b.CategoryID,
		Description: b.Description,
		OccurredAt:  b.OccurredAt.Format(time.RFC3339),
		CreatedAt:   b.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   b.UpdatedAt.Format(time.RFC3339),
	}
}

// CreateBill godoc
// @Summary Create transaction
// @Tags Transactions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param request body CreateBillRequest true "Transaction payload"
// @Success 201 {object} BillResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/transactions [post]
// @Router /api/bills [post]
func (h *Handler) CreateBill(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	req := new(CreateBillRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	amount, occurredAt, err := parseBillPayload(req.Amount, req.OccurredAt)
	if err != nil {
		return err
	}

	bill, err := h.S.Bill.CreateBill(c.Context(), userID, services.CreateBillInput{
		Type:         req.Type,
		Amount:       amount,
		Currency:     req.Currency,
		CategoryID:   req.CategoryID,
		CategoryName: req.CategoryName,
		Description:  req.Description,
		OccurredAt:   occurredAt,
	})
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(toBillResponse(*bill))
}

// BatchCreateBills godoc
// @Summary Create multiple transactions atomically
// @Tags Transactions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param request body BatchCreateBillsRequest true "Transactions payload"
// @Success 201 {array} BillResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/transactions/batch [post]
// @Router /api/bills/batch [post]
func (h *Handler) BatchCreateBills(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	req := new(BatchCreateBillsRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	inputs := make([]services.CreateBillInput, 0, len(req.Items))
	for i, item := range req.Items {
		amount, occurredAt, err := parseBillPayload(item.Amount, item.OccurredAt)
		if err != nil {
			return services.IndexBatchError(i, err)
		}
		inputs = append(inputs, services.CreateBillInput{
			Type:         item.Type,
			Amount:       amount,
			Currency:     item.Currency,
			CategoryID:   item.CategoryID,
			CategoryName: item.CategoryName,
			Description:  item.Description,
			OccurredAt:   occurredAt,
		})
	}

	bills, err := h.S.Bill.BatchCreateBills(c.Context(), userID, inputs)
	if err != nil {
		return err
	}

	response := make([]BillResponse, 0, len(bills))
	for _, bill := range bills {
		response = append(response, toBillResponse(bill))
	}
	return c.Status(fiber.StatusCreated).JSON(response)
}

// GetBill godoc
// @Summary Get transaction
// @Tags Transactions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param id path int true "Transaction ID"
// @Success 200 {object} BillResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/transactions/{id} [get]
// @Router /api/bills/{id} [get]
func (h *Handler) GetBill(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	bill, err := h.S.Bill.GetBill(c.Context(), userID, id)
	if err != nil {
		return err
	}

	return c.JSON(toBillResponse(*bill))
}

// ListBills godoc
// @Summary List transactions
// @Tags Transactions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param limit query int false "Page size" default(20)
// @Param offset query int false "Offset" default(0)
// @Param type query int false "Transaction type: 1 expense, 2 income"
// @Param category_id query int false "Category ID"
// @Param currency query string false "Currency code"
// @Param keyword query string false "Case-insensitive substring match on description"
// @Param start_time query string false "Start datetime (RFC3339)"
// @Param end_time query string false "End datetime (RFC3339)"
// @Success 200 {array} BillResponse
// @Failure 401 {object} ErrorResponse
// @Router /api/transactions [get]
// @Router /api/bills [get]
func (h *Handler) ListBills(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	var billType *int16
	if raw := strings.TrimSpace(c.Query("type")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 16)
		if err != nil {
			return errorx.ErrParamsInvalid
		}
		t := int16(parsed)
		billType = &t
	}
	var categoryID *int64
	if raw := strings.TrimSpace(c.Query("category_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return errorx.ErrParamsInvalid
		}
		categoryID = &parsed
	}
	var currency *string
	if raw := strings.TrimSpace(c.Query("currency")); raw != "" {
		currency = &raw
	}
	var keyword *string
	if raw := strings.TrimSpace(c.Query("keyword")); raw != "" {
		keyword = &raw
	}
	startTime, err := parseOptionalDateTime(c.Query("start_time"))
	if err != nil {
		return err
	}
	endTime, err := parseOptionalDateTime(c.Query("end_time"))
	if err != nil {
		return err
	}

	bills, err := h.S.Bill.ListBills(c.Context(), userID, services.ListBillsInput{
		Type:       billType,
		CategoryID: categoryID,
		Currency:   currency,
		Keyword:    keyword,
		StartTime:  startTime,
		EndTime:    endTime,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return err
	}

	response := make([]BillResponse, 0, len(bills))
	for _, bill := range bills {
		response = append(response, toBillResponse(bill))
	}

	return c.JSON(response)
}

// UpdateBill godoc
// @Summary Update transaction
// @Tags Transactions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param id path int true "Transaction ID"
// @Param request body UpdateBillRequest true "Transaction payload"
// @Success 200 {object} BillResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/transactions/{id} [put]
// @Router /api/bills/{id} [put]
func (h *Handler) UpdateBill(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	req := new(UpdateBillRequest)
	if err := c.Bind().Body(req); err != nil {
		return errorx.ErrParamsInvalid
	}

	amount, occurredAt, err := parseBillPayload(req.Amount, req.OccurredAt)
	if err != nil {
		return err
	}

	bill, err := h.S.Bill.UpdateBill(c.Context(), userID, services.UpdateBillInput{
		ID:           id,
		Type:         req.Type,
		Amount:       amount,
		Currency:     req.Currency,
		CategoryID:   req.CategoryID,
		CategoryName: req.CategoryName,
		Description:  req.Description,
		OccurredAt:   occurredAt,
	})
	if err != nil {
		return err
	}

	return c.JSON(toBillResponse(*bill))
}

// DeleteBill godoc
// @Summary Delete transaction
// @Tags Transactions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param id path int true "Transaction ID"
// @Success 204
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/transactions/{id} [delete]
// @Router /api/bills/{id} [delete]
func (h *Handler) DeleteBill(c fiber.Ctx) error {
	userID, err := middleware.UserID(c)
	if err != nil {
		return err
	}

	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return errorx.ErrParamsInvalid
	}

	if err := h.S.Bill.DeleteBill(c.Context(), userID, id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func parseBillPayload(amountRaw, occurredAtRaw string) (decimal.Decimal, time.Time, error) {
	amount, err := decimal.NewFromString(strings.TrimSpace(amountRaw))
	if err != nil {
		return decimal.Zero, time.Time{}, errorx.New(400, "Amount must be a valid decimal")
	}

	occurredAt, err := parseRequiredDateTime(occurredAtRaw)
	if err != nil {
		return decimal.Zero, time.Time{}, err
	}
	return amount, occurredAt, nil
}

// parseRequiredDateTime returns the zero time for an empty value; the bill
// service then fills in today's date using the user's timezone.
func parseRequiredDateTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		return parsed, nil
	}
	return time.Time{}, errorx.New(400, "OccurredAt must be a valid datetime")
}

func parseOptionalDateTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parsed, err := parseRequiredDateTime(raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
