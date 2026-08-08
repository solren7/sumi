// Package csvimport turns a CSV file into transaction records ready to be posted
// to the API. It is pure logic — no database, no HTTP, no filesystem — so every
// quirk it handles is covered by fast unit tests.
//
// The package deliberately does not try to recognise specific bank or wallet
// exports. Column layouts change and there is always one more format; instead the
// caller supplies a column mapping, which an agent can produce after looking at
// the file's header. What stays here is the mechanical work an agent should not do
// by hand: decoding, skipping preamble rows, parsing every amount and date exactly,
// and reporting the row number of anything it cannot convert.
package csvimport

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Field names the caller may map columns onto.
const (
	FieldDate     = "date"
	FieldAmount   = "amount"
	FieldCategory = "category"
	FieldType     = "type"
	FieldNote     = "note"
	FieldCurrency = "currency"
)

// Record is one parsed row, in the shape the CLI sends to the batch endpoint.
type Record struct {
	Row      int    // 1-based row number in the original file, for error messages
	Date     string // YYYY-MM-DD, empty means "let the server use today"
	Amount   string // decimal string, always positive
	Category string
	Type     string // "expense" or "income"
	Note     string
	Currency string
}

// RowError is a row that could not be converted.
type RowError struct {
	Row    int
	Reason string
	Raw    []string
}

func (e RowError) Error() string {
	return fmt.Sprintf("row %d: %s", e.Row, e.Reason)
}

// Options controls parsing. Only Mapping is normally required.
type Options struct {
	// Mapping maps a field name (see the Field* constants) to a column, either by
	// header name or by 1-based index ("3"). Fields absent from the mapping fall
	// back to a header whose name equals the field name.
	Mapping map[string]string
	// CategoryMap renames source categories, e.g. "餐饮美食" -> "吃". Lookups are
	// case-insensitive and trimmed. A source value with no entry is passed through.
	CategoryMap map[string]string
	// DefaultCategory is used when a row resolves to no category at all.
	DefaultCategory string
	// DefaultType applies when no type column is mapped. Defaults to "expense".
	DefaultType string
	// SkipRows drops leading lines before the header, for exports that begin with
	// human-readable preamble.
	SkipRows int
	// Delimiter defaults to comma.
	Delimiter rune
	// Encoding is "utf8", "gbk", or "" to auto-detect.
	Encoding string
	// SkipInvalid keeps going past a bad row instead of failing the file.
	SkipInvalid bool
	// KeepZeroAmount retains rows whose amount is zero; they are dropped by default
	// because wallet exports use 0.00 for non-monetary entries.
	KeepZeroAmount bool
}

// Result is what a parse produced.
type Result struct {
	Records []Record
	Errors  []RowError
	// Header is the resolved header row, useful for reporting.
	Header []string
	// Skipped counts rows dropped for being empty or zero-amount, which are not
	// errors.
	Skipped int
}

// Parse decodes and converts the whole file. With SkipInvalid false it returns an
// error on the first unconvertible row, so a caller can refuse to import a file it
// does not fully understand.
func Parse(raw []byte, opts Options) (*Result, error) {
	text, err := decode(raw, opts.Encoding)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1 // wallet exports pad rows unevenly
	reader.LazyQuotes = true
	if opts.Delimiter != 0 {
		reader.Comma = opts.Delimiter
	}

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("cannot read CSV: %w", err)
	}
	if opts.SkipRows > 0 {
		if opts.SkipRows >= len(rows) {
			return nil, fmt.Errorf("--skip-rows %d leaves no rows (file has %d)", opts.SkipRows, len(rows))
		}
		rows = rows[opts.SkipRows:]
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("the file has no rows")
	}

	header := trimAll(rows[0])
	columns, err := resolveColumns(header, opts.Mapping)
	if err != nil {
		return nil, err
	}

	result := &Result{Header: header}
	for i, row := range rows[1:] {
		// +1 for the header, +1 for 1-based numbering, plus any skipped preamble.
		rowNumber := opts.SkipRows + i + 2

		if isBlank(row) {
			result.Skipped++
			continue
		}

		record, err := convert(row, rowNumber, columns, opts)
		if err != nil {
			rowErr := RowError{Row: rowNumber, Reason: err.Error(), Raw: row}
			if !opts.SkipInvalid {
				return nil, rowErr
			}
			result.Errors = append(result.Errors, rowErr)
			continue
		}
		if record == nil {
			result.Skipped++
			continue
		}
		result.Records = append(result.Records, *record)
	}

	return result, nil
}

// decode strips a UTF-8 BOM and converts GBK when asked or when the bytes are not
// valid UTF-8. Chinese wallet exports are commonly GBK.
func decode(raw []byte, encoding string) (string, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "auto":
		if utf8.Valid(raw) {
			return string(raw), nil
		}
		return fromGBK(raw)
	case "utf8", "utf-8":
		if !utf8.Valid(raw) {
			return "", fmt.Errorf("the file is not valid UTF-8; try --encoding gbk")
		}
		return string(raw), nil
	case "gbk", "gb18030", "gb2312":
		return fromGBK(raw)
	default:
		return "", fmt.Errorf("unsupported --encoding %q: use utf8 or gbk", encoding)
	}
}

func fromGBK(raw []byte) (string, error) {
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(raw), simplifiedchinese.GB18030.NewDecoder()))
	if err != nil {
		return "", fmt.Errorf("cannot decode as GBK: %w", err)
	}
	return string(decoded), nil
}

// resolveColumns turns the mapping into column indexes. A mapping value is either
// a header name or a 1-based column number.
func resolveColumns(header []string, mapping map[string]string) (map[string]int, error) {
	lookup := make(map[string]int, len(header))
	for i, name := range header {
		lookup[strings.ToLower(name)] = i
	}

	columns := make(map[string]int, len(mapping))
	for _, field := range []string{FieldDate, FieldAmount, FieldCategory, FieldType, FieldNote, FieldCurrency} {
		spec, mapped := mapping[field]
		if !mapped {
			// Fall back to a header literally named after the field.
			if index, ok := lookup[field]; ok {
				columns[field] = index
			}
			continue
		}

		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		if index, ok := parseColumnNumber(spec, len(header)); ok {
			columns[field] = index
			continue
		}
		index, ok := lookup[strings.ToLower(spec)]
		if !ok {
			return nil, fmt.Errorf("column %q (mapped to %s) is not in the header: %s",
				spec, field, strings.Join(header, ", "))
		}
		columns[field] = index
	}

	if _, ok := columns[FieldAmount]; !ok {
		return nil, fmt.Errorf("no amount column: map one with --map amount=<column>")
	}
	return columns, nil
}

func parseColumnNumber(spec string, columnCount int) (int, bool) {
	if spec == "" {
		return 0, false
	}
	for _, r := range spec {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	number := 0
	for _, r := range spec {
		number = number*10 + int(r-'0')
	}
	if number < 1 || number > columnCount {
		return 0, false
	}
	return number - 1, true
}

// convert builds one record. A nil record with no error means "drop this row".
func convert(row []string, rowNumber int, columns map[string]int, opts Options) (*Record, error) {
	value := func(field string) string {
		index, ok := columns[field]
		if !ok || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}

	amountRaw := value(FieldAmount)
	if amountRaw == "" {
		return nil, fmt.Errorf("empty amount")
	}
	amount, err := parseAmount(amountRaw)
	if err != nil {
		return nil, err
	}

	// Drop before interpreting anything else: wallet exports use a 0.00 amount for
	// non-monetary entries whose type column says something like "不计收支", and
	// those rows must be skipped rather than rejected for an unknown direction.
	if amount.IsZero() && !opts.KeepZeroAmount {
		return nil, nil
	}

	billType := opts.DefaultType
	if billType == "" {
		billType = "expense"
	}
	if raw := value(FieldType); raw != "" {
		resolved, err := parseType(raw)
		if err != nil {
			return nil, err
		}
		billType = resolved
	}

	// A negative amount means the opposite direction, not a negative bill.
	if amount.IsNegative() {
		amount = amount.Abs()
		if value(FieldType) == "" {
			billType = "expense"
		}
	}

	date := ""
	if raw := value(FieldDate); raw != "" {
		parsed, err := parseDate(raw)
		if err != nil {
			return nil, err
		}
		date = parsed
	}

	category := value(FieldCategory)
	if mapped, ok := lookupCategory(opts.CategoryMap, category); ok {
		category = mapped
	}
	if category == "" {
		category = opts.DefaultCategory
	}
	if category == "" {
		return nil, fmt.Errorf("no category: map a column with --map category=<column> or set --default-category")
	}

	return &Record{
		Row:      rowNumber,
		Date:     date,
		Amount:   amount.String(),
		Category: category,
		Type:     billType,
		Note:     value(FieldNote),
		Currency: value(FieldCurrency),
	}, nil
}

func lookupCategory(categoryMap map[string]string, source string) (string, bool) {
	if len(categoryMap) == 0 {
		return "", false
	}
	wanted := strings.ToLower(strings.TrimSpace(source))
	for from, to := range categoryMap {
		if strings.ToLower(strings.TrimSpace(from)) == wanted {
			return to, true
		}
	}
	return "", false
}

// parseAmount accepts the decorations real exports carry: currency symbols,
// thousands separators, and parentheses for negatives.
func parseAmount(raw string) (decimal.Decimal, error) {
	cleaned := strings.TrimSpace(raw)

	negative := false
	if strings.HasPrefix(cleaned, "(") && strings.HasSuffix(cleaned, ")") {
		negative = true
		cleaned = strings.TrimSuffix(strings.TrimPrefix(cleaned, "("), ")")
	}

	cleaned = strings.NewReplacer(
		"¥", "", "￥", "", "$", "", "€", "", "£", "",
		",", "", " ", "", " ", "", "元", "",
	).Replace(cleaned)

	if cleaned == "" {
		return decimal.Zero, fmt.Errorf("empty amount")
	}

	amount, err := decimal.NewFromString(cleaned)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%q is not a number", raw)
	}
	if negative {
		amount = amount.Neg()
	}
	return amount, nil
}

// dateLayouts covers what exports actually emit, most specific first.
var dateLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
	"2006/01/02",
	"2006.01.02",
	"20060102",
	"01/02/2006",
}

// parseDate normalizes to YYYY-MM-DD. Only the calendar day matters: the server
// stores a bill against a date, not a timestamp.
func parseDate(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.NewReplacer("年", "-", "月", "-", "日", "").Replace(cleaned)
	cleaned = strings.TrimSuffix(cleaned, "-")

	for _, layout := range dateLayouts {
		if parsed, err := time.Parse(layout, cleaned); err == nil {
			return parsed.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("%q is not a recognized date (expected e.g. 2026-08-07)", raw)
}

// parseType maps the many words exports use onto expense/income.
func parseType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "expense", "支出", "支", "1", "-", "out", "debit", "付款", "消费":
		return "expense", nil
	case "income", "收入", "收", "2", "+", "in", "credit", "收款", "入账":
		return "income", nil
	default:
		return "", fmt.Errorf("%q is neither expense nor income", raw)
	}
}

func trimAll(row []string) []string {
	trimmed := make([]string, len(row))
	for i, value := range row {
		trimmed[i] = strings.TrimSpace(value)
	}
	return trimmed
}

func isBlank(row []string) bool {
	for _, value := range row {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}
