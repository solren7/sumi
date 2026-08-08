// Package domain holds the business rules that need no infrastructure: no
// database, no cache, no HTTP. Being a separate package makes that a compile-time
// guarantee rather than a convention, and lets these rules be unit-tested in
// milliseconds without docker (see rules_test.go).
//
// Dependencies are limited on purpose, and arch_test.go enforces the limit:
//
//   - the standard library;
//   - shopspring/decimal and google/uuid, both pure value types with no I/O;
//   - pkg/errorx, which is an error type carrying an HTTP status and performs no
//     I/O. Carrying a status code this deep is a deliberate compromise: the
//     alternative is a sentinel-error-to-HTTP translation layer in every service,
//     which costs more than it clarifies for this project.
//
// Rules that need to read data (does this category belong to the user? is this
// email taken?) stay in the service layer — only the decision that follows the
// read is modelled here, e.g. MatchSecondLevelCategory.
package domain

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sumi/pkg/errorx"

	"github.com/shopspring/decimal"
)

// Transaction types as stored in bills.type and categories.type.
const (
	TypeExpense int16 = 1
	TypeIncome  int16 = 2
)

const (
	// MaxBatchSize bounds one batch insert so a malformed request cannot open an
	// unbounded transaction. It is sized for a CSV import (one file, one
	// transaction); an interactive client recording a handful of bills stays far
	// below it.
	MaxBatchSize = 1000
	// MaxDescriptionLength matches the practical limit enforced on bill notes.
	MaxDescriptionLength = 500
	// MaxCategoryNameLength matches categories.name VARCHAR(50).
	MaxCategoryNameLength = 50
	// MaxUsernameLength matches the limit enforced at registration.
	MaxUsernameLength = 64
	// MinPasswordLength and MaxPasswordLength bound the password bcrypt will hash.
	MinPasswordLength = 8
	MaxPasswordLength = 128
	// CurrencyCodeLength is the ISO 4217 alphabetic code length.
	CurrencyCodeLength = 3
)

// ValidateBillType rejects a type outside the expense/income pair.
func ValidateBillType(billType int16) error {
	if billType != TypeExpense && billType != TypeIncome {
		return errorx.New(400, "Type must be 1 or 2")
	}
	return nil
}

// ValidateAmount requires a strictly positive amount; direction is carried by the
// type, never by a negative number.
func ValidateAmount(amount decimal.Decimal) error {
	if !amount.GreaterThan(decimal.Zero) {
		return errorx.New(400, "Amount must be greater than 0")
	}
	return nil
}

// NormalizeCurrency upper-cases and trims a currency code. An empty result means
// the caller should fall back to the account default; it is not an error here.
func NormalizeCurrency(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// ValidateCurrency checks an already-normalized code.
func ValidateCurrency(normalized string) error {
	if len(normalized) != CurrencyCodeLength {
		return errorx.New(400, "Currency must be a 3-letter code")
	}
	return nil
}

// NormalizeDescription trims a bill note and enforces its length.
func NormalizeDescription(raw string) (string, error) {
	description := strings.TrimSpace(raw)
	if len(description) > MaxDescriptionLength {
		return "", errorx.New(400, "Description must be at most 500 characters")
	}
	return description, nil
}

// ValidateBatchSize enforces both ends of a batch request.
func ValidateBatchSize(count int) error {
	if count == 0 {
		return errorx.New(400, "At least one transaction is required")
	}
	if count > MaxBatchSize {
		return errorx.Newf(400, "At most %d transactions per batch", MaxBatchSize)
	}
	return nil
}

// TodayIn returns midnight of the current day as seen from the given timezone,
// expressed as UTC midnight of that calendar day so it matches how an explicit
// "YYYY-MM-DD" payload is parsed.
//
// This is why an omitted date must be resolved server-side: a UTC-based client
// would otherwise file an evening bill under the previous day.
func TodayIn(timezone string) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
}

// KeywordPattern turns a raw search keyword into an ILIKE pattern, escaping the
// wildcards so user text is matched literally. Without this a keyword of "%"
// would match every row.
func KeywordPattern(raw string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(raw)
	return "%" + escaped + "%"
}

// CategoryCandidate is the minimum a category must expose for name resolution.
type CategoryCandidate struct {
	ID    int64
	Name  string
	Level int16
}

// RequireCategoryReference rejects a bill that identifies no category at all,
// rather than silently defaulting to one.
func RequireCategoryReference(categoryID int64, categoryName string) error {
	if categoryID <= 0 && strings.TrimSpace(categoryName) == "" {
		return errorx.New(400, "Either category_id or category_name is required")
	}
	return nil
}

// MatchSecondLevelCategory resolves a category name against the candidates of one
// type. Only second-level categories are considered, because a bill may not
// reference a top-level one; a name matching several is reported rather than
// guessed, which is what keeps name-based recording predictable.
func MatchSecondLevelCategory(candidates []CategoryCandidate, name string, billType int16) (int64, error) {
	wanted := strings.TrimSpace(name)

	matches := make([]CategoryCandidate, 0, 2)
	for _, candidate := range candidates {
		if candidate.Level == 2 && strings.EqualFold(strings.TrimSpace(candidate.Name), wanted) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return 0, errorx.Newf(400, "Category %q not found for this type; list valid names via GET /api/categories?type=%d", wanted, billType)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, strconv.FormatInt(match.ID, 10))
		}
		return 0, errorx.Newf(400, "Category name %q is ambiguous, use category_id instead (candidates: %s)", wanted, strings.Join(ids, ", "))
	}
}

// ValidateCategoryName trims and bounds a new category name. The length is
// counted in runes because the column limit is 50 characters, not 50 bytes.
func ValidateCategoryName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errorx.New(400, "Name is required")
	}
	if utf8.RuneCountInString(name) > MaxCategoryNameLength {
		return "", errorx.New(400, "Name must be at most 50 characters")
	}
	return name, nil
}

// ValidateAPIKeyName trims and requires an API key label.
func ValidateAPIKeyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errorx.New(400, "API key name is required")
	}
	return name, nil
}

// ValidateCredentials enforces the registration rules that need no database.
// Whether the email is already taken is a service-level concern.
func ValidateCredentials(email, password string) error {
	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return errorx.New(400, "Email and password are required")
	}
	if len(password) < MinPasswordLength {
		return errorx.New(400, "Password must be at least 8 characters")
	}
	if len(password) > MaxPasswordLength {
		return errorx.New(400, "Password must be at most 128 characters")
	}
	return nil
}

// ValidateUsername bounds an optional display name.
func ValidateUsername(raw string) error {
	if len(raw) > MaxUsernameLength {
		return errorx.New(400, "Username must be at most 64 characters")
	}
	return nil
}
