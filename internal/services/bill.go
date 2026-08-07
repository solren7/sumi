package services

import (
	"context"
	"strings"
	"time"

	"sumi/config"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

type BillService struct {
	q     *dbgen.Queries
	cfg   *config.Config
	rdb   redis.UniversalClient
	stats *StatsService
}

func NewBillService(q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient, stats *StatsService) *BillService {
	return &BillService{q: q, cfg: cfg, rdb: rdb, stats: stats}
}

type CreateBillInput struct {
	Type        int16
	Amount      decimal.Decimal
	Currency    string
	CategoryID  int64
	Description string
	OccurredAt  time.Time
}

type UpdateBillInput struct {
	ID          int64
	Type        int16
	Amount      decimal.Decimal
	Currency    string
	CategoryID  int64
	Description string
	OccurredAt  time.Time
}

type ListBillsInput struct {
	Type       *int16
	CategoryID *int64
	Currency   *string
	StartTime  *time.Time
	EndTime    *time.Time
	Limit      int
	Offset     int
}

func (s *BillService) CreateBill(ctx context.Context, userID uuid.UUID, input CreateBillInput) (*dbgen.Bill, error) {
	currency, err := validateBillInput(input.Type, input.Amount, input.Currency)
	if err != nil {
		return nil, err
	}

	if err := s.validateCategory(ctx, userID, input.CategoryID, input.Type); err != nil {
		return nil, err
	}

	description := strings.TrimSpace(input.Description)
	if len(description) > 500 {
		return nil, errorx.New(400, "Description must be at most 500 characters")
	}

	bill, err := s.q.CreateBill(ctx, dbgen.CreateBillParams{
		UserID:      userID,
		Type:        input.Type,
		Amount:      input.Amount,
		Currency:    currency,
		CategoryID:  input.CategoryID,
		Description: description,
		OccurredAt:  input.OccurredAt,
	})
	if err != nil {
		return nil, err
	}

	s.stats.InvalidateMonthCache(ctx, userID, bill.OccurredAt)
	return &bill, nil
}

func (s *BillService) GetBill(ctx context.Context, userID uuid.UUID, billID int64) (*dbgen.Bill, error) {
	bill, err := s.q.GetBillByID(ctx, dbgen.GetBillByIDParams{
		ID:     billID,
		UserID: userID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errorx.ErrNotFound
		}
		return nil, err
	}
	return &bill, nil
}

func (s *BillService) ListBills(ctx context.Context, userID uuid.UUID, input ListBillsInput) ([]dbgen.Bill, error) {
	params := dbgen.ListBillsParams{
		UserID:      userID,
		Type:        input.Type,
		CategoryID:  input.CategoryID,
		Currency:    normalizeOptionalCurrency(input.Currency),
		StartTime:   toNullableTimestamptz(input.StartTime),
		EndTime:     toNullableTimestamptz(input.EndTime),
		LimitCount:  int32(max(input.Limit, 1)),
		OffsetCount: int32(max(input.Offset, 0)),
	}

	bills, err := s.q.ListBills(ctx, params)
	if err != nil {
		return nil, err
	}
	if bills == nil {
		return []dbgen.Bill{}, nil
	}
	return bills, nil
}

func (s *BillService) UpdateBill(ctx context.Context, userID uuid.UUID, input UpdateBillInput) (*dbgen.Bill, error) {
	existing, err := s.GetBill(ctx, userID, input.ID)
	if err != nil {
		return nil, err
	}

	currency, err := validateBillInput(input.Type, input.Amount, input.Currency)
	if err != nil {
		return nil, err
	}
	if err := s.validateCategory(ctx, userID, input.CategoryID, input.Type); err != nil {
		return nil, err
	}

	description := strings.TrimSpace(input.Description)
	if len(description) > 500 {
		return nil, errorx.New(400, "Description must be at most 500 characters")
	}

	bill, err := s.q.UpdateBill(ctx, dbgen.UpdateBillParams{
		ID:          input.ID,
		Type:        input.Type,
		Amount:      input.Amount,
		Currency:    currency,
		CategoryID:  input.CategoryID,
		Description: description,
		OccurredAt:  input.OccurredAt,
		UserID:      userID,
	})
	if err != nil {
		return nil, err
	}

	s.stats.InvalidateMonthCache(ctx, userID, existing.OccurredAt)
	s.stats.InvalidateMonthCache(ctx, userID, bill.OccurredAt)
	return &bill, nil
}

func (s *BillService) DeleteBill(ctx context.Context, userID uuid.UUID, billID int64) error {
	existing, err := s.GetBill(ctx, userID, billID)
	if err != nil {
		return err
	}

	if err := s.q.DeleteBill(ctx, dbgen.DeleteBillParams{
		ID:     billID,
		UserID: userID,
	}); err != nil {
		return err
	}

	s.stats.InvalidateMonthCache(ctx, userID, existing.OccurredAt)
	return nil
}

func (s *BillService) validateCategory(ctx context.Context, userID uuid.UUID, categoryID int64, billType int16) error {
	category, err := s.q.GetCategoryByIDAndUser(ctx, dbgen.GetCategoryByIDAndUserParams{
		ID:     categoryID,
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return errorx.New(400, "Category not found")
		}
		return err
	}
	if !category.IsActive {
		return errorx.New(400, "Category is inactive")
	}
	if category.Level != 2 {
		return errorx.New(400, "Only second-level categories are allowed")
	}
	if category.Type != billType {
		return errorx.New(400, "Category type does not match bill type")
	}
	return nil
}

func validateBillInput(billType int16, amount decimal.Decimal, currency string) (string, error) {
	if billType != 1 && billType != 2 {
		return "", errorx.New(400, "Type must be 1 or 2")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return "", errorx.New(400, "Amount must be greater than 0")
	}

	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if len(normalized) != 3 {
		return "", errorx.New(400, "Currency must be a 3-letter code")
	}
	return normalized, nil
}

func normalizeOptionalCurrency(currency *string) *string {
	if currency == nil {
		return nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(*currency))
	if normalized == "" {
		return nil
	}
	return &normalized
}

func toNullableTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func max(v, minValue int) int {
	if v < minValue {
		return minValue
	}
	return v
}
