package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"sumi/config"
	"sumi/internal/cache"
	"sumi/internal/database"
	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"
	"sumi/internal/services"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// These tests drive the real HTTP stack against a real PostgreSQL and Redis,
// because the logic most likely to break lives in SQL and in cache invalidation:
// timezone-dependent dates, ILIKE escaping and transaction rollback cannot be
// verified against a mock.
//
// Run them with:
//
//	make test-integration
//
// They skip when TEST_DB_DSN is unset so `go test ./...` stays green without docker.

type testEnv struct {
	t     *testing.T
	app   *fiber.App
	pool  *pgxpool.Pool
	rdb   redis.UniversalClient
	cfg   *config.Config
	token string
	key   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if dsn == "" {
		t.Skip("TEST_DB_DSN is not set; run `make test-integration`")
	}
	redisURL := strings.TrimSpace(os.Getenv("TEST_REDIS_URL"))
	if redisURL == "" {
		redisURL = "redis://localhost:56379/0"
	}

	cfg := config.NewConfig()
	cfg.DBDSN = dsn
	cfg.RedisConfig.RedisURL = redisURL
	cfg.AutoMigrate = false
	cfg.LogFormat = "console"

	pool, err := database.NewPool(t.Context(), cfg)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	rdb, err := database.NewRedis(t.Context(), cfg)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	svc := services.NewService(services.Deps{Pool: pool, Queries: dbgen.New(pool), Config: cfg, Redis: rdb})
	env := &testEnv{t: t, app: NewFiberApp(cfg, svc), pool: pool, rdb: rdb, cfg: cfg}
	env.registerFreshUser()
	return env
}

// do issues one request and returns the status plus the raw body.
func (e *testEnv) do(method, path, auth string, body any) (int, []byte) {
	e.t.Helper()

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, path, payload)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch auth {
	case "jwt":
		req.Header.Set("Authorization", "Bearer "+e.token)
	case "key":
		req.Header.Set("X-API-Key", e.key)
	}

	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second})
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, raw
}

// mustDo fails the test unless the status matches, decoding into target if given.
func (e *testEnv) mustDo(method, path, auth string, body any, wantStatus int, target any) {
	e.t.Helper()
	status, raw := e.do(method, path, auth, body)
	if status != wantStatus {
		e.t.Fatalf("%s %s: status %d (want %d), body: %s", method, path, status, wantStatus, raw)
	}
	if target != nil {
		if err := json.Unmarshal(raw, target); err != nil {
			e.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
}

// errorMessage returns the server's message for a request expected to fail.
func (e *testEnv) errorMessage(method, path, auth string, body any, wantStatus int) string {
	e.t.Helper()
	status, raw := e.do(method, path, auth, body)
	if status != wantStatus {
		e.t.Fatalf("%s %s: status %d (want %d), body: %s", method, path, status, wantStatus, raw)
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		e.t.Fatalf("decode error body %s: %v", raw, err)
	}
	return envelope.Message
}

func (e *testEnv) registerFreshUser() {
	e.t.Helper()

	email := fmt.Sprintf("test-%d-%s@example.com", time.Now().UnixNano(), strings.ToLower(e.t.Name()))
	email = strings.ReplaceAll(email, "/", "-")

	var session struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	e.mustDo("POST", "/api/auth/register", "", map[string]string{
		"email":    email,
		"password": "integration-password-123",
		"username": "integration",
	}, http.StatusCreated, &session)
	e.token = session.AccessToken

	var created struct {
		Key string `json:"key"`
	}
	e.mustDo("POST", "/api/api-keys/", "jwt", map[string]any{
		"name": "integration",
		"scopes": []string{
			"transactions:read", "transactions:write", "transactions:update",
			"transactions:delete", "categories:read", "categories:write", "stats:read",
		},
	}, http.StatusCreated, &created)
	e.key = created.Key
}

type billBody struct {
	ID          int64  `json:"id"`
	Type        int16  `json:"type"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	CategoryID  int64  `json:"category_id"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
}

// TestCategoryNameResolution covers the name lookup an AI client relies on, and
// the two ways it must refuse to guess.
func TestCategoryNameResolution(t *testing.T) {
	env := newTestEnv(t)

	var bill billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "25.50", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &bill)
	if bill.CategoryID == 0 {
		t.Fatal("category_name did not resolve to a category id")
	}

	// "其他" exists at level 1 and level 2; bills may only use level 2, so this
	// must resolve rather than fail as ambiguous.
	var other billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "10", "category_name": "其他", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &other)

	var tree []domain.CategoryNode
	env.mustDo("GET", "/api/categories/?type=1", "key", nil, http.StatusOK, &tree)
	levelTwo := map[int64]bool{}
	for _, parent := range tree {
		for _, child := range parent.Children {
			levelTwo[child.ID] = true
		}
	}
	if !levelTwo[other.CategoryID] {
		t.Fatalf("其他 resolved to id %d, which is not a second-level category", other.CategoryID)
	}

	// A first-level name is not a valid bill category.
	if msg := env.errorMessage("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "10", "category_name": "必要", "occurred_at": "2026-08-07",
	}, http.StatusBadRequest); !strings.Contains(msg, "not found") {
		t.Fatalf("first-level name should be rejected, got %q", msg)
	}

	// An expense category name must not satisfy an income bill.
	if msg := env.errorMessage("POST", "/api/transactions/", "key", map[string]any{
		"type": 2, "amount": "10", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusBadRequest); !strings.Contains(msg, "not found for this type") {
		t.Fatalf("cross-type name should be rejected, got %q", msg)
	}

	// Neither id nor name is a client error, not a silent default.
	if msg := env.errorMessage("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "10", "occurred_at": "2026-08-07",
	}, http.StatusBadRequest); !strings.Contains(msg, "category_id or category_name") {
		t.Fatalf("missing category should be rejected, got %q", msg)
	}
}

// TestDuplicateCategoryNameRejected pins the invariant that makes name lookup
// unambiguous: a level-2 name is unique per type, even under a different parent.
func TestDuplicateCategoryNameRejected(t *testing.T) {
	env := newTestEnv(t)

	env.mustDo("POST", "/api/categories/", "key", map[string]any{
		"name": "宠物", "type": 1, "parent_name": "非必要",
	}, http.StatusCreated, nil)

	status, raw := env.do("POST", "/api/categories/", "key", map[string]any{
		"name": "宠物", "type": 1, "parent_name": "必要",
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate level-2 name under another parent: status %d (want 409), body %s", status, raw)
	}

	// The new category is usable by name straight away, i.e. the cache was cleared.
	var bill billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "200", "category_name": "宠物", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &bill)
	if bill.CategoryID == 0 {
		t.Fatal("newly created category was not usable by name")
	}
}

// TestBatchIsAtomic asserts a rejected item leaves nothing behind and names its index.
func TestBatchIsAtomic(t *testing.T) {
	env := newTestEnv(t)

	var created []billBody
	env.mustDo("POST", "/api/transactions/batch", "key", map[string]any{
		"items": []map[string]any{
			{"type": 1, "amount": "12", "category_name": "吃", "description": "早饭", "occurred_at": "2026-08-07"},
			{"type": 1, "amount": "30", "category_name": "行", "description": "打车", "occurred_at": "2026-08-07"},
		},
	}, http.StatusCreated, &created)
	if len(created) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(created))
	}

	msg := env.errorMessage("POST", "/api/transactions/batch", "key", map[string]any{
		"items": []map[string]any{
			{"type": 1, "amount": "1", "category_name": "吃", "description": "回滚A", "occurred_at": "2026-08-07"},
			{"type": 1, "amount": "2", "category_name": "不存在的分类", "description": "回滚B", "occurred_at": "2026-08-07"},
		},
	}, http.StatusBadRequest)
	if !strings.Contains(msg, "items[1]") {
		t.Fatalf("error should name the failing index, got %q", msg)
	}

	var remaining []billBody
	env.mustDo("GET", "/api/transactions/?keyword=回滚", "key", nil, http.StatusOK, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("failed batch was not rolled back: %d rows persisted", len(remaining))
	}

	// An empty batch is a client error, not a silent no-op.
	if msg := env.errorMessage("POST", "/api/transactions/batch", "key", map[string]any{
		"items": []map[string]any{},
	}, http.StatusBadRequest); !strings.Contains(msg, "At least one") {
		t.Errorf("empty batch should be rejected, got %q", msg)
	}

	// The size cap bounds how large a transaction one request can open.
	oversized := make([]map[string]any, domain.MaxBatchSize+1)
	for i := range oversized {
		oversized[i] = map[string]any{
			"type": 1, "amount": "1", "category_name": "吃", "occurred_at": "2026-08-07",
		}
	}
	if msg := env.errorMessage("POST", "/api/transactions/batch", "key", map[string]any{
		"items": oversized,
	}, http.StatusBadRequest); !strings.Contains(msg, "At most") {
		t.Errorf("oversized batch should be rejected, got %q", msg)
	}
}

// TestOmittedDateUsesUserTimezone is the regression test for the bug where a
// UTC-based client filed an evening bill under the previous day. The server must
// resolve "today" from users.timezone, not from its own clock's zone.
//
// It drives two zones 25 hours apart, so their calendar day always differs no
// matter when the suite runs: a server that used its own zone would return the
// same date for both and fail.
func TestOmittedDateUsesUserTimezone(t *testing.T) {
	env := newTestEnv(t)
	userID := userIDOf(t, env)

	seen := make(map[string]string)
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Niue"} { // UTC+14 and UTC-11
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatalf("load %s (is tzdata present?): %v", zone, err)
		}

		if _, err := env.pool.Exec(t.Context(),
			"UPDATE users SET timezone = $1 WHERE id = $2", zone, userID); err != nil {
			t.Fatalf("set timezone %s: %v", zone, err)
		}

		var bill billBody
		env.mustDo("POST", "/api/transactions/", "key", map[string]any{
			"type": 1, "amount": "9.99", "category_name": "吃",
		}, http.StatusCreated, &bill)

		want := time.Now().In(loc).Format("2006-01-02")
		if got := bill.OccurredAt[:10]; got != want {
			t.Errorf("timezone %s: occurred_at = %s, want %s", zone, got, want)
		}
		seen[zone] = bill.OccurredAt[:10]
	}

	if seen["Pacific/Kiritimati"] == seen["Pacific/Niue"] {
		t.Errorf("both zones produced %s; the server is ignoring users.timezone",
			seen["Pacific/Kiritimati"])
	}
}

// TestOmittedCurrencyUsesUserDefault covers the other server-side default.
func TestOmittedCurrencyUsesUserDefault(t *testing.T) {
	env := newTestEnv(t)

	var bill billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "5", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &bill)
	if bill.Currency != env.cfg.DefaultCurrency {
		t.Fatalf("currency = %q, want %q", bill.Currency, env.cfg.DefaultCurrency)
	}

	// An explicit lowercase code is still normalized.
	var explicit billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "5", "category_name": "吃", "currency": "usd", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &explicit)
	if explicit.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", explicit.Currency)
	}
}

// TestKeywordSearchEscapesWildcards makes sure a "%" in the keyword matches
// literally instead of returning every row.
func TestKeywordSearchEscapesWildcards(t *testing.T) {
	env := newTestEnv(t)

	for _, note := range []string{"午饭", "买了一件外套", "折扣50%off"} {
		env.mustDo("POST", "/api/transactions/", "key", map[string]any{
			"type": 1, "amount": "10", "category_name": "吃", "description": note, "occurred_at": "2026-08-07",
		}, http.StatusCreated, nil)
	}

	cases := []struct {
		keyword string
		want    int
	}{
		{"午饭", 1},
		{"外套", 1},
		{"%", 1}, // matches only the literal "50%off" row, not all three
		{"_", 0}, // underscore is literal too
		{"没有", 0},
	}
	for _, tc := range cases {
		var rows []billBody
		env.mustDo("GET", "/api/transactions/?keyword="+url.QueryEscape(tc.keyword), "key", nil, http.StatusOK, &rows)
		if len(rows) != tc.want {
			t.Errorf("keyword %q matched %d rows, want %d", tc.keyword, len(rows), tc.want)
		}
	}
}

// TestStatsCacheInvalidatedOnWrite is the guard for the cache-invalidation rule:
// every write path must clear the month's cached stats, otherwise reads go stale.
func TestStatsCacheInvalidatedOnWrite(t *testing.T) {
	env := newTestEnv(t)

	readTotal := func() string {
		var stats domain.MonthlyStatsOutput
		env.mustDo("GET", "/api/stats/monthly?month=2026-08", "key", nil, http.StatusOK, &stats)
		for _, item := range stats.Items {
			if item.Currency == env.cfg.DefaultCurrency {
				return item.TotalExpense
			}
		}
		return "0"
	}

	var bill billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "100", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &bill)

	if got := readTotal(); got != "100.00" {
		t.Fatalf("after create: expense = %s, want 100.00", got)
	}
	// Reading populated the cache; each following write must invalidate it.
	if err := env.rdb.Get(t.Context(), cache.MonthlyStatsKey(userIDOf(t, env), "2026-08")).Err(); err != nil {
		t.Fatalf("expected the monthly stats to be cached after a read: %v", err)
	}

	env.mustDo("PUT", fmt.Sprintf("/api/transactions/%d", bill.ID), "key", map[string]any{
		"type": 1, "amount": "150", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusOK, nil)
	if got := readTotal(); got != "150.00" {
		t.Fatalf("after update: expense = %s, want 150.00 (stale cache?)", got)
	}

	env.mustDo("POST", "/api/transactions/batch", "key", map[string]any{
		"items": []map[string]any{
			{"type": 1, "amount": "50", "category_name": "吃", "occurred_at": "2026-08-07"},
		},
	}, http.StatusCreated, nil)
	if got := readTotal(); got != "200.00" {
		t.Fatalf("after batch: expense = %s, want 200.00 (stale cache?)", got)
	}

	env.mustDo("DELETE", fmt.Sprintf("/api/transactions/%d", bill.ID), "key", nil, http.StatusNoContent, nil)
	if got := readTotal(); got != "50.00" {
		t.Fatalf("after delete: expense = %s, want 50.00 (stale cache?)", got)
	}
}

// TestScopesAreEnforced checks the API-key scope gate per action.
func TestScopesAreEnforced(t *testing.T) {
	env := newTestEnv(t)

	var readOnly struct {
		Key string `json:"key"`
	}
	env.mustDo("POST", "/api/api-keys/", "jwt", map[string]any{
		"name": "read-only", "scopes": []string{"transactions:read"},
	}, http.StatusCreated, &readOnly)

	fullKey := env.key
	env.key = readOnly.Key
	defer func() { env.key = fullKey }()

	if status, raw := env.do("GET", "/api/transactions/", "key", nil); status != http.StatusOK {
		t.Fatalf("read scope should allow listing: status %d, %s", status, raw)
	}
	if status, _ := env.do("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "1", "category_name": "吃", "occurred_at": "2026-08-07",
	}); status != http.StatusForbidden {
		t.Fatalf("write without scope: status %d (want 403)", status)
	}
	if status, _ := env.do("POST", "/api/categories/", "key", map[string]any{
		"name": "x", "type": 1, "parent_name": "必要",
	}); status != http.StatusForbidden {
		t.Fatalf("category write without scope: status %d (want 403)", status)
	}
}

// TestUpdateReplacesWholeRow documents that PUT is a full replace: an omitted
// description is cleared. The CLI compensates by re-sending every field.
func TestUpdateReplacesWholeRow(t *testing.T) {
	env := newTestEnv(t)

	var bill billBody
	env.mustDo("POST", "/api/transactions/", "key", map[string]any{
		"type": 1, "amount": "20", "category_name": "吃", "description": "原备注", "occurred_at": "2026-08-07",
	}, http.StatusCreated, &bill)

	var updated billBody
	env.mustDo("PUT", fmt.Sprintf("/api/transactions/%d", bill.ID), "key", map[string]any{
		"type": 1, "amount": "30", "category_name": "吃", "occurred_at": "2026-08-07",
	}, http.StatusOK, &updated)

	if updated.Amount != "30" && updated.Amount != "30.00" {
		t.Fatalf("amount = %q, want 30", updated.Amount)
	}
	if updated.Description != "" {
		t.Fatalf("description = %q; PUT is a full replace so it should be cleared", updated.Description)
	}
}

func userIDOf(t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	var me struct {
		ID string `json:"id"`
	}
	env.mustDo("GET", "/api/auth/me", "jwt", nil, http.StatusOK, &me)
	parsed, err := uuid.Parse(me.ID)
	if err != nil {
		t.Fatalf("parse user id %q: %v", me.ID, err)
	}
	return parsed
}

// TestLargeBatchIsAtomicAndCheap covers what a CSV import actually does: several
// hundred rows in one transaction. It also pins the fix for the N+1 that a naive
// implementation has — the per-user and per-category-tree reads must happen once
// for the whole batch, not once per row.
func TestLargeBatchIsAtomicAndCheap(t *testing.T) {
	env := newTestEnv(t)

	const rows = 400
	items := make([]map[string]any, 0, rows)
	for i := 0; i < rows; i++ {
		items = append(items, map[string]any{
			"type": 1, "amount": "1.00", "category_name": "吃",
			"description": fmt.Sprintf("import-%d", i), "occurred_at": "2026-08-07",
		})
	}

	var created []billBody
	env.mustDo("POST", "/api/transactions/batch", "key",
		map[string]any{"items": items}, http.StatusCreated, &created)
	if len(created) != rows {
		t.Fatalf("created %d rows, want %d", len(created), rows)
	}

	// The whole batch must be one transaction, so a single bad row at the end
	// rolls back every earlier row too.
	items = append(items, map[string]any{
		"type": 1, "amount": "1.00", "category_name": "没有这个分类", "occurred_at": "2026-08-07",
	})
	for i := range items {
		if description, ok := items[i]["description"]; ok {
			items[i]["description"] = "rollback-" + fmt.Sprint(description)
		}
	}
	msg := env.errorMessage("POST", "/api/transactions/batch", "key",
		map[string]any{"items": items}, http.StatusBadRequest)
	if !strings.Contains(msg, fmt.Sprintf("items[%d]", rows)) {
		t.Errorf("error should name the failing index %d, got %q", rows, msg)
	}

	var leftovers []billBody
	env.mustDo("GET", "/api/transactions/?keyword=rollback-&limit=1000", "key", nil, http.StatusOK, &leftovers)
	if len(leftovers) != 0 {
		t.Fatalf("a %d-row batch was not rolled back: %d rows persisted", len(items), len(leftovers))
	}

	// Stats must reflect only the successful batch.
	var stats domain.MonthlyStatsOutput
	env.mustDo("GET", "/api/stats/monthly?month=2026-08", "key", nil, http.StatusOK, &stats)
	for _, item := range stats.Items {
		if item.Currency == env.cfg.DefaultCurrency && item.TotalExpense != "400.00" {
			t.Errorf("total expense = %s, want 400.00", item.TotalExpense)
		}
	}
}
