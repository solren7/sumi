package services

import (
	"context"
	"encoding/json"
	"time"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/repository/dbgen"
	"sumi/pkg/utils"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

type StatsService struct {
	q   *dbgen.Queries
	cfg *config.Config
	rdb redis.UniversalClient
}

func NewStatsService(q *dbgen.Queries, cfg *config.Config, rdb redis.UniversalClient) *StatsService {
	return &StatsService{q: q, cfg: cfg, rdb: rdb}
}

type MonthlyStatsItem struct {
	Currency     string `json:"currency"`
	TotalIncome  string `json:"total_income"`
	TotalExpense string `json:"total_expense"`
	NetAmount    string `json:"net_amount"`
}

type MonthlyStatsOutput struct {
	Month string             `json:"month"`
	Items []MonthlyStatsItem `json:"items"`
}

type DailyStatsItem struct {
	Currency string `json:"currency"`
	Income   string `json:"income"`
	Expense  string `json:"expense"`
}

type DailyStatsDay struct {
	Date  string           `json:"date"`
	Items []DailyStatsItem `json:"items"`
}

type DailyStatsOutput struct {
	Month string          `json:"month"`
	Days  []DailyStatsDay `json:"days"`
}

type CategoryStatsItem struct {
	ParentCategoryID   *int64  `json:"parent_category_id,omitempty"`
	ParentCategoryName *string `json:"parent_category_name,omitempty"`
	CategoryID         int64   `json:"category_id"`
	CategoryName       string  `json:"category_name"`
	Currency           string  `json:"currency"`
	Amount             string  `json:"amount"`
}

type CategoryStatsOutput struct {
	Month string              `json:"month"`
	Type  int16               `json:"type"`
	Items []CategoryStatsItem `json:"items"`
}

func (s *StatsService) GetMonthlyStats(ctx context.Context, userID uuid.UUID, month string) (*MonthlyStatsOutput, error) {
	user, err := s.q.GetUserById(ctx, userID)
	if err != nil {
		return nil, err
	}

	monthKey, startTime, endTime, err := resolveMonthRange(month, user.Timezone)
	if err != nil {
		return nil, err
	}

	cacheKey := cache.MonthlyStatsKey(userID, monthKey)
	var output MonthlyStatsOutput
	if ok := s.getCached(ctx, cacheKey, &output); ok {
		return &output, nil
	}

	rows, err := s.q.GetMonthlyStats(ctx, dbgen.GetMonthlyStatsParams{
		UserID:    userID,
		StartTime: startTime,
		EndTime:   endTime,
	})
	if err != nil {
		return nil, err
	}

	items := make([]MonthlyStatsItem, 0, len(rows))
	for _, row := range rows {
		income := utils.NumericToString(row.TotalIncome)
		expense := utils.NumericToString(row.TotalExpense)
		items = append(items, MonthlyStatsItem{
			Currency:     row.Currency,
			TotalIncome:  income,
			TotalExpense: expense,
			NetAmount:    subtractDecimalStrings(income, expense),
		})
	}

	output = MonthlyStatsOutput{
		Month: monthKey,
		Items: items,
	}
	s.setCached(ctx, cacheKey, output, s.cfg.StatsCacheTTL)
	return &output, nil
}

func (s *StatsService) GetDailyStats(ctx context.Context, userID uuid.UUID, month string) (*DailyStatsOutput, error) {
	user, err := s.q.GetUserById(ctx, userID)
	if err != nil {
		return nil, err
	}

	monthKey, startTime, endTime, err := resolveMonthRange(month, user.Timezone)
	if err != nil {
		return nil, err
	}

	cacheKey := cache.DailyStatsKey(userID, monthKey)
	var output DailyStatsOutput
	if ok := s.getCached(ctx, cacheKey, &output); ok {
		return &output, nil
	}

	rows, err := s.q.GetDailyStats(ctx, dbgen.GetDailyStatsParams{
		UserTimezone: user.Timezone,
		UserID:       userID,
		StartTime:    startTime,
		EndTime:      endTime,
	})
	if err != nil {
		return nil, err
	}

	days := make([]DailyStatsDay, 0)
	dayIndex := map[string]int{}
	for _, row := range rows {
		dateStr := row.Date.Time.Format("2006-01-02")
		idx, exists := dayIndex[dateStr]
		if !exists {
			idx = len(days)
			dayIndex[dateStr] = idx
			days = append(days, DailyStatsDay{
				Date:  dateStr,
				Items: []DailyStatsItem{},
			})
		}

		days[idx].Items = append(days[idx].Items, DailyStatsItem{
			Currency: row.Currency,
			Income:   utils.NumericToString(row.Income),
			Expense:  utils.NumericToString(row.Expense),
		})
	}

	output = DailyStatsOutput{
		Month: monthKey,
		Days:  days,
	}
	s.setCached(ctx, cacheKey, output, s.cfg.StatsCacheTTL)
	return &output, nil
}

func (s *StatsService) GetCategoryStats(ctx context.Context, userID uuid.UUID, month string, billType int16) (*CategoryStatsOutput, error) {
	user, err := s.q.GetUserById(ctx, userID)
	if err != nil {
		return nil, err
	}
	monthKey, startTime, endTime, err := resolveMonthRange(month, user.Timezone)
	if err != nil {
		return nil, err
	}

	cacheKey := cache.CategoryStatsKey(userID, monthKey, billType)
	var output CategoryStatsOutput
	if ok := s.getCached(ctx, cacheKey, &output); ok {
		return &output, nil
	}

	rows, err := s.q.GetCategoryStats(ctx, dbgen.GetCategoryStatsParams{
		UserID:    userID,
		Type:      billType,
		StartTime: startTime,
		EndTime:   endTime,
	})
	if err != nil {
		return nil, err
	}

	items := make([]CategoryStatsItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, CategoryStatsItem{
			ParentCategoryID:   row.ParentCategoryID,
			ParentCategoryName: row.ParentCategoryName,
			CategoryID:         row.CategoryID,
			CategoryName:       row.CategoryName,
			Currency:           row.Currency,
			Amount:             utils.NumericToString(row.Amount),
		})
	}

	output = CategoryStatsOutput{
		Month: monthKey,
		Type:  billType,
		Items: items,
	}
	s.setCached(ctx, cacheKey, output, s.cfg.StatsCacheTTL)
	return &output, nil
}

func (s *StatsService) InvalidateMonthCache(ctx context.Context, userID uuid.UUID, occurredAt time.Time) {
	loc := occurredAt.Location()
	monthKey := cache.MonthKey(occurredAt, loc)
	keys := []string{
		cache.MonthlyStatsKey(userID, monthKey),
		cache.DailyStatsKey(userID, monthKey),
		cache.CategoryStatsKey(userID, monthKey, 1),
		cache.CategoryStatsKey(userID, monthKey, 2),
	}
	_ = s.rdb.Del(ctx, keys...).Err()
}

func resolveMonthRange(month, timezone string) (string, time.Time, time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}

	var start time.Time
	if month == "" {
		now := time.Now().In(loc)
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	} else {
		parsed, err := time.ParseInLocation("2006-01", month, loc)
		if err != nil {
			return "", time.Time{}, time.Time{}, err
		}
		start = time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, loc)
	}

	end := start.AddDate(0, 1, 0)
	return start.Format("2006-01"), start.UTC(), end.UTC(), nil
}

func subtractDecimalStrings(a, b string) string {
	left, err := decimal.NewFromString(a)
	if err != nil {
		left = decimal.Zero
	}
	right, err := decimal.NewFromString(b)
	if err != nil {
		right = decimal.Zero
	}
	return left.Sub(right).StringFixed(2)
}

func (s *StatsService) getCached(ctx context.Context, key string, out any) bool {
	raw, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	return json.Unmarshal([]byte(raw), out) == nil
}

func (s *StatsService) setCached(ctx context.Context, key string, value any, ttl time.Duration) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = s.rdb.Set(ctx, key, payload, ttl).Err()
}
