package services

// Payload preparation: turns a request into a row-ready value by applying the
// infrastructure-free rules in internal/domain plus the lookups that need the
// database (per-user defaults, category ownership).

import (
	"context"
	"errors"
	"strings"
	"time"

	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/errorx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// billLookups caches the per-user reads that every row of a batch would otherwise
// repeat. A 1000-row import would issue a thousand identical user lookups and a
// thousand identical category-tree lookups without it.
//
// Its lifetime is one request, so it cannot serve stale data across requests, and
// inside a batch it shares the transaction's *dbgen.Queries.
type billLookups struct {
	user       *dbgen.User
	categories map[int16][]dbgen.Category
}

func newBillLookups() *billLookups {
	return &billLookups{categories: make(map[int16][]dbgen.Category, 2)}
}

func (l *billLookups) getUser(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) (*dbgen.User, error) {
	if l.user != nil {
		return l.user, nil
	}
	user, err := q.GetUserById(ctx, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errorx.ErrUnauthorized
		}
		return nil, err
	}
	l.user = &user
	return l.user, nil
}

func (l *billLookups) getCategories(ctx context.Context, q *dbgen.Queries, userID uuid.UUID, billType int16) ([]dbgen.Category, error) {
	if cached, ok := l.categories[billType]; ok {
		return cached, nil
	}
	rows, err := q.ListCategoriesByUserAndType(ctx, dbgen.ListCategoriesByUserAndTypeParams{
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		Type:   billType,
	})
	if err != nil {
		return nil, err
	}
	l.categories[billType] = rows
	return rows, nil
}

// prepareBill validates and normalizes one payload. It takes a *dbgen.Queries so
// batch inserts can resolve categories inside their own transaction.
func (s *BillService) prepareBill(ctx context.Context, q *dbgen.Queries, lookups *billLookups, userID uuid.UUID, input CreateBillInput) (*preparedBill, error) {
	if err := domain.ValidateBillType(input.Type); err != nil {
		return nil, err
	}
	if err := domain.ValidateAmount(input.Amount); err != nil {
		return nil, err
	}
	description, err := domain.NormalizeDescription(input.Description)
	if err != nil {
		return nil, err
	}

	// Currency and date both fall back to per-user settings, so fetch the user at
	// most once and only when a fallback is actually needed.
	currency := domain.NormalizeCurrency(input.Currency)
	occurredAt := input.OccurredAt
	if currency == "" || occurredAt.IsZero() {
		user, err := lookups.getUser(ctx, q, userID)
		if err != nil {
			return nil, err
		}
		if currency == "" {
			currency = domain.NormalizeCurrency(user.DefaultCurrency)
		}
		if occurredAt.IsZero() {
			resolved, err := domain.TodayIn(user.Timezone)
			if err != nil {
				return nil, err
			}
			occurredAt = resolved
		}
	}
	if err := domain.ValidateCurrency(currency); err != nil {
		return nil, err
	}

	categoryID, err := s.resolveCategory(ctx, q, lookups, userID, input.CategoryID, input.CategoryName, input.Type)
	if err != nil {
		return nil, err
	}

	return &preparedBill{
		Type:        input.Type,
		Amount:      input.Amount,
		Currency:    currency,
		CategoryID:  categoryID,
		Description: description,
		OccurredAt:  occurredAt,
	}, nil
}

// resolveCategory accepts either an explicit ID or a category name. Names are
// matched against second-level categories only, since those are the only ones a
// bill may reference; an ambiguous name is rejected rather than guessed.
func (s *BillService) resolveCategory(ctx context.Context, q *dbgen.Queries, lookups *billLookups, userID uuid.UUID, categoryID int64, categoryName string, billType int16) (int64, error) {
	if err := domain.RequireCategoryReference(categoryID, categoryName); err != nil {
		return 0, err
	}

	if categoryID > 0 {
		category, err := q.GetCategoryByIDAndUser(ctx, dbgen.GetCategoryByIDAndUserParams{
			ID:     categoryID,
			UserID: pgtype.UUID{Bytes: userID, Valid: true},
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				return 0, errorx.New(400, "Category not found")
			}
			return 0, err
		}
		if !category.IsActive {
			return 0, errorx.New(400, "Category is inactive")
		}
		if category.Level != 2 {
			return 0, errorx.New(400, "Only second-level categories are allowed")
		}
		if category.Type != billType {
			return 0, errorx.New(400, "Category type does not match bill type")
		}
		return category.ID, nil
	}

	rows, err := lookups.getCategories(ctx, q, userID, billType)
	if err != nil {
		return 0, err
	}

	candidates := make([]domain.CategoryCandidate, 0, len(rows))
	for _, category := range rows {
		candidates = append(candidates, domain.CategoryCandidate{
			ID:    category.ID,
			Name:  category.Name,
			Level: category.Level,
		})
	}
	return domain.MatchSecondLevelCategory(candidates, categoryName, billType)
}

// descriptionLikePattern builds the ILIKE pattern for an optional keyword.
func descriptionLikePattern(keyword *string) *string {
	if keyword == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*keyword)
	if trimmed == "" {
		return nil
	}
	pattern := domain.KeywordPattern(trimmed)
	return &pattern
}

func IndexBatchError(index int, err error) error {
	var typed *errorx.Error
	if errors.As(err, &typed) {
		return errorx.Newf(typed.Code, "items[%d]: %s", index, typed.Message)
	}
	return err
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
