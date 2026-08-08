package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// The types the service layer returns and the handlers consume. They are plain
// data with no behaviour and no infrastructure dependency, which is what lets them
// sit at the centre of the dependency graph.
//
// Three notes on what is deliberately absent or present:
//
//   - No secrets. dbgen.ApiKey carries KeyHash and dbgen.User carries
//     PasswordHash; neither appears here, so they cannot reach an HTTP response.
//   - No database-specific type. pgtype.Timestamptz becomes *time.Time.
//   - JSON tags on the tree and stats types are a conscious compromise: handlers
//     serialize them directly, and defining a parallel set of response structs for
//     seven aggregate shapes would cost more boilerplate than the purity is worth.
//     Bill/APIKey/User carry no tags because handlers map those explicitly.
//
// Conversion from sqlc rows lives in internal/services/mapping.go, since that is
// the only layer allowed to know about dbgen.

type Bill struct {
	ID          int64
	UserID      uuid.UUID
	Type        int16
	Amount      decimal.Decimal
	Currency    string
	CategoryID  int64
	Description string
	OccurredAt  time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type APIKey struct {
	ID         uuid.UUID
	Name       string
	Prefix     string
	Scopes     []string
	Status     string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

type User struct {
	ID              uuid.UUID
	Email           string
	Username        string
	DefaultCurrency string
	Timezone        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CategoryNode is one node of a user's two-level category tree.
type CategoryNode struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Type     int16          `json:"type"`
	Level    int16          `json:"level"`
	ParentID *int64         `json:"parent_id,omitempty"`
	Children []CategoryNode `json:"children,omitempty"`
}

// Amounts in the stats types are strings because they are decimals rendered for
// transport: a float would silently lose cents.

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
