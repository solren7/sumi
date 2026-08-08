package services

import (
	"context"
	"time"

	"sumi/config"
	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

type preparedBill struct {
	Type        int16
	Amount      decimal.Decimal
	Currency    string
	CategoryID  int64
	Description string
	OccurredAt  time.Time
}

type BillService struct {
	pool  *pgxpool.Pool
	q     *dbgen.Queries
	cfg   *config.Config
	rdb   redis.UniversalClient
	stats *StatsService
}

func NewBillService(deps Deps, stats *StatsService) *BillService {
	return &BillService{pool: deps.Pool, q: deps.Queries, cfg: deps.Config, rdb: deps.Redis, stats: stats}
}

type CreateBillInput struct {
	Type         int16
	Amount       decimal.Decimal
	Currency     string
	CategoryID   int64
	CategoryName string
	Description  string
	OccurredAt   time.Time
}

type UpdateBillInput struct {
	ID           int64
	Type         int16
	Amount       decimal.Decimal
	Currency     string
	CategoryID   int64
	CategoryName string
	Description  string
	OccurredAt   time.Time
}

type ListBillsInput struct {
	Type       *int16
	CategoryID *int64
	Currency   *string
	Keyword    *string
	StartTime  *time.Time
	EndTime    *time.Time
	Limit      int
	Offset     int
}

func (s *BillService) CreateBill(ctx context.Context, userID uuid.UUID, input CreateBillInput) (*domain.Bill, error) {
	prepared, err := s.prepareBill(ctx, s.q, newBillLookups(), userID, input)
	if err != nil {
		return nil, err
	}

	bill, err := s.q.CreateBill(ctx, dbgen.CreateBillParams{
		UserID:      userID,
		Type:        prepared.Type,
		Amount:      prepared.Amount,
		Currency:    prepared.Currency,
		CategoryID:  prepared.CategoryID,
		Description: prepared.Description,
		OccurredAt:  prepared.OccurredAt,
	})
	if err != nil {
		return nil, err
	}

	s.invalidateStatsFor(ctx, userID, bill.OccurredAt)
	created := billFromRow(bill)
	return &created, nil
}

// BatchCreateBills inserts every input in a single transaction: one invalid item
// rejects the whole batch, so a partially recorded batch can never be observed.
func (s *BillService) BatchCreateBills(ctx context.Context, userID uuid.UUID, inputs []CreateBillInput) ([]domain.Bill, error) {
	if err := domain.ValidateBatchSize(len(inputs)); err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	txQueries := s.q.WithTx(tx)
	// One cache for the whole batch: the user and category tree are read once.
	lookups := newBillLookups()
	rows := make([]dbgen.Bill, 0, len(inputs))
	for i, input := range inputs {
		prepared, err := s.prepareBill(ctx, txQueries, lookups, userID, input)
		if err != nil {
			return nil, IndexBatchError(i, err)
		}

		bill, err := txQueries.CreateBill(ctx, dbgen.CreateBillParams{
			UserID:      userID,
			Type:        prepared.Type,
			Amount:      prepared.Amount,
			Currency:    prepared.Currency,
			CategoryID:  prepared.CategoryID,
			Description: prepared.Description,
			OccurredAt:  prepared.OccurredAt,
		})
		if err != nil {
			return nil, err
		}
		rows = append(rows, bill)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	touched := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		touched = append(touched, row.OccurredAt)
	}
	s.invalidateStatsFor(ctx, userID, touched...)
	return billsFromRows(rows), nil
}

func (s *BillService) UpdateBill(ctx context.Context, userID uuid.UUID, input UpdateBillInput) (*domain.Bill, error) {
	existing, err := s.GetBill(ctx, userID, input.ID)
	if err != nil {
		return nil, err
	}

	prepared, err := s.prepareBill(ctx, s.q, newBillLookups(), userID, CreateBillInput{
		Type:         input.Type,
		Amount:       input.Amount,
		Currency:     input.Currency,
		CategoryID:   input.CategoryID,
		CategoryName: input.CategoryName,
		Description:  input.Description,
		OccurredAt:   input.OccurredAt,
	})
	if err != nil {
		return nil, err
	}

	bill, err := s.q.UpdateBill(ctx, dbgen.UpdateBillParams{
		ID:          input.ID,
		Type:        prepared.Type,
		Amount:      prepared.Amount,
		Currency:    prepared.Currency,
		CategoryID:  prepared.CategoryID,
		Description: prepared.Description,
		OccurredAt:  prepared.OccurredAt,
		UserID:      userID,
	})
	if err != nil {
		return nil, err
	}

	s.invalidateStatsFor(ctx, userID, existing.OccurredAt, bill.OccurredAt)
	updated := billFromRow(bill)
	return &updated, nil
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

	s.invalidateStatsFor(ctx, userID, existing.OccurredAt)
	return nil
}

// invalidateStatsFor drops the cached stats for every month a write touched.
//
// Every path that inserts, updates or deletes a bill must call this — otherwise
// /api/stats keeps serving pre-write totals. Routing all of them through one
// method keeps the rule in a single place; TestStatsCacheInvalidatedOnWrite is
// what actually catches a path that forgets.
func (s *BillService) invalidateStatsFor(ctx context.Context, userID uuid.UUID, months ...time.Time) {
	for _, month := range months {
		s.stats.InvalidateMonthCache(ctx, userID, month)
	}
}
