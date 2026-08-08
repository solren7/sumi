package services

// Read paths for transactions. Kept apart from the write paths so the cache
// invalidation rule (see invalidateStatsFor) applies to one file only.

import (
	"context"

	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *BillService) GetBill(ctx context.Context, userID uuid.UUID, billID int64) (*domain.Bill, error) {
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
	found := billFromRow(bill)
	return &found, nil
}

func (s *BillService) ListBills(ctx context.Context, userID uuid.UUID, input ListBillsInput) ([]domain.Bill, error) {
	params := dbgen.ListBillsParams{
		UserID:      userID,
		Type:        input.Type,
		CategoryID:  input.CategoryID,
		Currency:    normalizeOptionalCurrency(input.Currency),
		Keyword:     descriptionLikePattern(input.Keyword),
		StartTime:   toNullableTimestamptz(input.StartTime),
		EndTime:     toNullableTimestamptz(input.EndTime),
		LimitCount:  int32(max(input.Limit, 1)),
		OffsetCount: int32(max(input.Offset, 0)),
	}

	rows, err := s.q.ListBills(ctx, params)
	if err != nil {
		return nil, err
	}
	return billsFromRows(rows), nil
}
