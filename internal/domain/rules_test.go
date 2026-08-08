package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"sumi/pkg/errorx"

	"github.com/shopspring/decimal"
)

// These run without a database, a cache or docker: that is the point of keeping
// this package free of infrastructure. They cover the boundaries the integration
// suite only touches from one side.

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var typed *errorx.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected an *errorx.Error, got %T: %v", err, err)
	}
	return typed.Code
}

func TestValidateBillType(t *testing.T) {
	for _, valid := range []int16{TypeExpense, TypeIncome} {
		if err := ValidateBillType(valid); err != nil {
			t.Errorf("type %d should be valid: %v", valid, err)
		}
	}
	for _, invalid := range []int16{0, 3, -1, 100} {
		err := ValidateBillType(invalid)
		if err == nil {
			t.Errorf("type %d should be rejected", invalid)
			continue
		}
		if code := statusOf(t, err); code != 400 {
			t.Errorf("type %d: status %d, want 400", invalid, code)
		}
	}
}

func TestValidateAmount(t *testing.T) {
	cases := []struct {
		amount string
		ok     bool
	}{
		{"0.01", true},
		{"25.50", true},
		{"999999.99", true},
		{"0", false},  // zero is not a transaction
		{"-1", false}, // direction is carried by the type, not the sign
		{"-0.01", false},
	}
	for _, tc := range cases {
		amount, err := decimal.NewFromString(tc.amount)
		if err != nil {
			t.Fatalf("bad test fixture %q: %v", tc.amount, err)
		}
		got := ValidateAmount(amount) == nil
		if got != tc.ok {
			t.Errorf("amount %s: accepted=%v, want %v", tc.amount, got, tc.ok)
		}
	}
}

func TestCurrencyNormalizationAndValidation(t *testing.T) {
	if got := NormalizeCurrency("  usd "); got != "USD" {
		t.Errorf("NormalizeCurrency(\"  usd \") = %q, want USD", got)
	}
	// An empty code is not an error here; the caller substitutes the account default.
	if got := NormalizeCurrency("   "); got != "" {
		t.Errorf("blank currency should normalize to empty, got %q", got)
	}

	if err := ValidateCurrency("CNY"); err != nil {
		t.Errorf("CNY should be valid: %v", err)
	}
	for _, invalid := range []string{"", "CN", "CNYY"} {
		if err := ValidateCurrency(invalid); err == nil {
			t.Errorf("currency %q should be rejected", invalid)
		}
	}
}

func TestNormalizeDescription(t *testing.T) {
	got, err := NormalizeDescription("  午饭  ")
	if err != nil || got != "午饭" {
		t.Errorf("got (%q, %v), want (\"午饭\", nil)", got, err)
	}

	if _, err := NormalizeDescription(strings.Repeat("a", MaxDescriptionLength)); err != nil {
		t.Errorf("exactly the limit should be accepted: %v", err)
	}
	if _, err := NormalizeDescription(strings.Repeat("a", MaxDescriptionLength+1)); err == nil {
		t.Error("one past the limit should be rejected")
	}
}

func TestValidateBatchSize(t *testing.T) {
	if err := ValidateBatchSize(0); err == nil {
		t.Error("an empty batch should be rejected, not treated as a no-op")
	}
	if err := ValidateBatchSize(1); err != nil {
		t.Errorf("a single item should be accepted: %v", err)
	}
	if err := ValidateBatchSize(MaxBatchSize); err != nil {
		t.Errorf("exactly the cap should be accepted: %v", err)
	}
	if err := ValidateBatchSize(MaxBatchSize + 1); err == nil {
		t.Error("one past the cap should be rejected")
	}
}

// TestKeywordPattern is the guard against a keyword of "%" matching every row.
func TestKeywordPattern(t *testing.T) {
	cases := map[string]string{
		"午饭":     "%午饭%",
		"50%off": `%50\%off%`,
		"a_b":    `%a\_b%`,
		`back\s`: `%back\\s%`,
	}
	for input, want := range cases {
		if got := KeywordPattern(input); got != want {
			t.Errorf("KeywordPattern(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestTodayIn pins the rule that "today" comes from the given zone, using two
// zones 25 hours apart so their calendar day always differs.
func TestTodayIn(t *testing.T) {
	east, err := TodayIn("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Fatalf("load Pacific/Kiritimati (tzdata missing?): %v", err)
	}
	west, err := TodayIn("Pacific/Niue") // UTC-11
	if err != nil {
		t.Fatalf("load Pacific/Niue: %v", err)
	}

	if east.Equal(west) {
		t.Error("zones 25 hours apart produced the same day; the timezone is being ignored")
	}
	if east.Location() != time.UTC {
		t.Errorf("result should be UTC midnight, got location %v", east.Location())
	}
	for _, got := range []time.Time{east, west} {
		if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
			t.Errorf("expected midnight, got %s", got)
		}
	}

	if _, err := TodayIn("Not/AZone"); err == nil {
		t.Error("an unknown timezone should be an error, not a silent UTC fallback")
	}
}

func TestRequireCategoryReference(t *testing.T) {
	if err := RequireCategoryReference(0, ""); err == nil {
		t.Error("neither id nor name should be rejected")
	}
	if err := RequireCategoryReference(0, "  "); err == nil {
		t.Error("a blank name is still no reference")
	}
	if err := RequireCategoryReference(7, ""); err != nil {
		t.Errorf("an id alone is enough: %v", err)
	}
	if err := RequireCategoryReference(0, "吃"); err != nil {
		t.Errorf("a name alone is enough: %v", err)
	}
}

// TestMatchSecondLevelCategory covers the resolution an AI client depends on:
// level-2 only, case-insensitive, and refusing to guess between duplicates.
func TestMatchSecondLevelCategory(t *testing.T) {
	// Mirrors the seeded expense tree, where "其他" exists at both levels.
	candidates := []CategoryCandidate{
		{ID: 1, Name: "必要", Level: 1},
		{ID: 2, Name: "吃", Level: 2},
		{ID: 3, Name: "行", Level: 2},
		{ID: 4, Name: "其他", Level: 1},
		{ID: 5, Name: "其他", Level: 2},
	}

	if id, err := MatchSecondLevelCategory(candidates, "吃", TypeExpense); err != nil || id != 2 {
		t.Errorf("吃 -> (%d, %v), want (2, nil)", id, err)
	}
	// The level-1 namesake must not shadow the level-2 one.
	if id, err := MatchSecondLevelCategory(candidates, "其他", TypeExpense); err != nil || id != 5 {
		t.Errorf("其他 -> (%d, %v), want (5, nil)", id, err)
	}
	if id, err := MatchSecondLevelCategory(candidates, "  吃  ", TypeExpense); err != nil || id != 2 {
		t.Errorf("surrounding spaces should be ignored: (%d, %v)", id, err)
	}

	// A top-level name is not a usable bill category.
	_, err := MatchSecondLevelCategory(candidates, "必要", TypeExpense)
	if err == nil {
		t.Error("a first-level name should not resolve")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("message should say not found, got %q", err)
	}

	if _, err := MatchSecondLevelCategory(candidates, "不存在", TypeExpense); err == nil {
		t.Error("an unknown name should not resolve")
	}

	// Duplicates are reported with their ids instead of picking one.
	duplicated := append(candidates, CategoryCandidate{ID: 9, Name: "吃", Level: 2})
	_, err = MatchSecondLevelCategory(duplicated, "吃", TypeExpense)
	if err == nil {
		t.Fatal("an ambiguous name should be an error")
	}
	for _, want := range []string{"ambiguous", "2", "9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity message %q should mention %q", err, want)
		}
	}

	if _, err := MatchSecondLevelCategory(nil, "吃", TypeExpense); err == nil {
		t.Error("no candidates should not resolve")
	}
}

func TestValidateCategoryName(t *testing.T) {
	if got, err := ValidateCategoryName("  宠物 "); err != nil || got != "宠物" {
		t.Errorf("got (%q, %v), want (\"宠物\", nil)", got, err)
	}
	if _, err := ValidateCategoryName("   "); err == nil {
		t.Error("a blank name should be rejected")
	}
	// The limit is 50 characters, not 50 bytes: 50 CJK runes must be accepted.
	if _, err := ValidateCategoryName(strings.Repeat("猫", MaxCategoryNameLength)); err != nil {
		t.Errorf("50 multi-byte runes should be accepted: %v", err)
	}
	if _, err := ValidateCategoryName(strings.Repeat("猫", MaxCategoryNameLength+1)); err == nil {
		t.Error("51 runes should be rejected")
	}
}

func TestValidateCredentials(t *testing.T) {
	if err := ValidateCredentials("a@b.com", strings.Repeat("x", MinPasswordLength)); err != nil {
		t.Errorf("minimum-length password should be accepted: %v", err)
	}
	cases := []struct{ email, password string }{
		{"", "password123"},
		{"a@b.com", ""},
		{"  ", "password123"},
		{"a@b.com", strings.Repeat("x", MinPasswordLength-1)},
		{"a@b.com", strings.Repeat("x", MaxPasswordLength+1)},
	}
	for _, tc := range cases {
		if err := ValidateCredentials(tc.email, tc.password); err == nil {
			t.Errorf("email=%q password(len=%d) should be rejected", tc.email, len(tc.password))
		}
	}
}

func TestValidateUsername(t *testing.T) {
	if err := ValidateUsername(strings.Repeat("u", MaxUsernameLength)); err != nil {
		t.Errorf("exactly the limit should be accepted: %v", err)
	}
	if err := ValidateUsername(strings.Repeat("u", MaxUsernameLength+1)); err == nil {
		t.Error("one past the limit should be rejected")
	}
	if err := ValidateUsername(""); err != nil {
		t.Errorf("an empty username is allowed (it is derived from the email): %v", err)
	}
}

func TestValidateAPIKeyName(t *testing.T) {
	if got, err := ValidateAPIKeyName(" agent "); err != nil || got != "agent" {
		t.Errorf("got (%q, %v), want (\"agent\", nil)", got, err)
	}
	if _, err := ValidateAPIKeyName("  "); err == nil {
		t.Error("a blank key name should be rejected")
	}
}
