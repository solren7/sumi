package csvimport

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// No database, no files: everything here is the mechanical conversion an agent
// should not be doing by hand.

func TestParseCustomFormat(t *testing.T) {
	raw := []byte("date,amount,category,type,note\n" +
		"2026-08-07,25.50,吃,expense,午饭\n" +
		"2026-08-06,5000,工资,income,八月工资\n")

	result, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(result.Records))
	}

	first := result.Records[0]
	if first.Date != "2026-08-07" || first.Amount != "25.5" || first.Category != "吃" ||
		first.Type != "expense" || first.Note != "午饭" {
		t.Errorf("unexpected first record: %+v", first)
	}
	if result.Records[1].Type != "income" {
		t.Errorf("second row type = %q, want income", result.Records[1].Type)
	}
	// Row numbers must point at the original file so errors are actionable.
	if first.Row != 2 {
		t.Errorf("first data row should be row 2, got %d", first.Row)
	}
}

// TestParseWalletStyleExport is the shape an agent will most often hand over:
// Chinese headers, a preamble, currency symbols, a type column, and rows to drop.
func TestParseWalletStyleExport(t *testing.T) {
	raw := []byte("支付宝交易记录明细查询\n" +
		"账号:someone@example.com\n" +
		"起始日期:2026-08-01 终止日期:2026-08-31\n" +
		"交易时间,商品说明,金额,收/支,交易分类\n" +
		"2026-08-07 12:30:00,午餐,¥25.50,支出,餐饮美食\n" +
		"2026-08-06 09:00:00,地铁,￥3.00,支出,交通出行\n" +
		"2026-08-05 18:00:00,退款,¥0.00,不计收支,退款\n")

	result, err := Parse(raw, Options{
		SkipRows: 3,
		Mapping: map[string]string{
			FieldDate:     "交易时间",
			FieldNote:     "商品说明",
			FieldAmount:   "金额",
			FieldType:     "收/支",
			FieldCategory: "交易分类",
		},
		CategoryMap: map[string]string{"餐饮美食": "吃", "交通出行": "行"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(result.Records) != 2 {
		t.Fatalf("got %d records, want 2 (the ¥0.00 row should be dropped): %+v", len(result.Records), result.Records)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}

	want := []Record{
		{Row: 5, Date: "2026-08-07", Amount: "25.5", Category: "吃", Type: "expense", Note: "午餐"},
		{Row: 6, Date: "2026-08-06", Amount: "3", Category: "行", Type: "expense", Note: "地铁"},
	}
	for i, expected := range want {
		got := result.Records[i]
		if got != expected {
			t.Errorf("record %d = %+v, want %+v", i, got, expected)
		}
	}
}

func TestParseGBKEncodedFile(t *testing.T) {
	utf8Source := "交易时间,金额,交易分类\n2026-08-07,25.50,餐饮\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(utf8Source))
	if err != nil {
		t.Fatalf("encode fixture as GBK: %v", err)
	}

	opts := Options{Mapping: map[string]string{
		FieldDate: "交易时间", FieldAmount: "金额", FieldCategory: "交易分类",
	}}

	// Auto-detection must handle it without being told.
	result, err := Parse(encoded, opts)
	if err != nil {
		t.Fatalf("auto-detect GBK: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Category != "餐饮" {
		t.Fatalf("GBK decode produced %+v", result.Records)
	}

	// And an explicit flag must work too.
	opts.Encoding = "gbk"
	if _, err := Parse(encoded, opts); err != nil {
		t.Fatalf("explicit gbk: %v", err)
	}

	// Claiming UTF-8 for a GBK file must fail loudly, with a hint.
	opts.Encoding = "utf8"
	_, err = Parse(encoded, opts)
	if err == nil {
		t.Fatal("declaring utf8 for a GBK file should fail")
	}
	if !strings.Contains(err.Error(), "gbk") {
		t.Errorf("error should suggest --encoding gbk, got %q", err)
	}
}

func TestParseStripsUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("date,amount,category\n2026-08-07,10,吃\n")...)
	result, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(result.Records))
	}
	// A surviving BOM would corrupt the first header name and break the mapping.
	if result.Header[0] != "date" {
		t.Errorf("header[0] = %q, want date (BOM not stripped?)", result.Header[0])
	}
}

func TestParseAmountVariants(t *testing.T) {
	cases := map[string]string{
		"25.50":       "25.5",
		"¥25.50":      "25.5",
		`"￥1,234.56"`: "1234.56",
		`"1,234"`:     "1234",
		"25.50元":      "25.5",
		" 42 ":        "42",
		"(30.00)":     "30", // parentheses mean negative, normalized to positive expense
		"-15.75":      "15.75",
	}
	for input, want := range cases {
		raw := []byte("amount,category\n" + input + ",吃\n")
		result, err := Parse(raw, Options{})
		if err != nil {
			t.Errorf("amount %q: %v", input, err)
			continue
		}
		if len(result.Records) != 1 {
			t.Errorf("amount %q produced %d records", input, len(result.Records))
			continue
		}
		if got := result.Records[0].Amount; got != want {
			t.Errorf("amount %q -> %q, want %q", input, got, want)
		}
		// A negative source amount must never become a negative bill.
		if strings.HasPrefix(result.Records[0].Amount, "-") {
			t.Errorf("amount %q produced a negative bill", input)
		}
	}

	for _, bad := range []string{"abc", "¥", "--5"} {
		raw := []byte("amount,category\n" + bad + ",吃\n")
		if _, err := Parse(raw, Options{}); err == nil {
			t.Errorf("amount %q should be rejected", bad)
		}
	}
}

func TestParseDateVariants(t *testing.T) {
	cases := map[string]string{
		"2026-08-07":          "2026-08-07",
		"2026-08-07 12:30:00": "2026-08-07",
		"2026/08/07":          "2026-08-07",
		"2026/08/07 12:30":    "2026-08-07",
		"2026.08.07":          "2026-08-07",
		"20260807":            "2026-08-07",
		"2026年08月07日":         "2026-08-07",
	}
	for input, want := range cases {
		raw := []byte("date,amount,category\n" + input + ",10,吃\n")
		result, err := Parse(raw, Options{})
		if err != nil {
			t.Errorf("date %q: %v", input, err)
			continue
		}
		if got := result.Records[0].Date; got != want {
			t.Errorf("date %q -> %q, want %q", input, got, want)
		}
	}

	if _, err := Parse([]byte("date,amount,category\nnot-a-date,10,吃\n"), Options{}); err == nil {
		t.Error("an unparseable date should be rejected, not silently dropped")
	}
}

// TestMissingDateIsAllowed documents that an absent date defers to the server,
// which resolves "today" in the user's timezone.
func TestMissingDateIsAllowed(t *testing.T) {
	result, err := Parse([]byte("amount,category\n10,吃\n"), Options{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.Records[0].Date != "" {
		t.Errorf("date = %q, want empty so the server fills it in", result.Records[0].Date)
	}
}

func TestColumnMappingByIndex(t *testing.T) {
	// A file with no usable header names: map by 1-based column number instead.
	raw := []byte("a,b,c\n2026-08-07,25.50,吃\n")
	result, err := Parse(raw, Options{Mapping: map[string]string{
		FieldDate: "1", FieldAmount: "2", FieldCategory: "3",
	}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := result.Records[0]
	if got.Date != "2026-08-07" || got.Amount != "25.5" || got.Category != "吃" {
		t.Errorf("index mapping produced %+v", got)
	}
}

func TestUnknownMappedColumnIsReported(t *testing.T) {
	_, err := Parse([]byte("date,amount\n2026-08-07,10\n"), Options{
		Mapping: map[string]string{FieldAmount: "amount", FieldCategory: "不存在的列"},
	})
	if err == nil {
		t.Fatal("mapping a missing column should fail")
	}
	// The message must show what the header actually contains.
	for _, want := range []string{"不存在的列", "date", "amount"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestMissingAmountColumnIsReported(t *testing.T) {
	_, err := Parse([]byte("date,category\n2026-08-07,吃\n"), Options{})
	if err == nil {
		t.Fatal("a file with no amount column should fail")
	}
	if !strings.Contains(err.Error(), "amount") {
		t.Errorf("error should name the amount column, got %q", err)
	}
}

func TestMissingCategoryNeedsADefault(t *testing.T) {
	raw := []byte("date,amount\n2026-08-07,10\n")

	if _, err := Parse(raw, Options{}); err == nil {
		t.Fatal("a row with no category should fail rather than guess")
	}

	result, err := Parse(raw, Options{DefaultCategory: "其他"})
	if err != nil {
		t.Fatalf("with a default category: %v", err)
	}
	if result.Records[0].Category != "其他" {
		t.Errorf("category = %q, want 其他", result.Records[0].Category)
	}
}

// TestSkipInvalid is the tolerant mode: bad rows are collected, not fatal.
func TestSkipInvalid(t *testing.T) {
	raw := []byte("date,amount,category\n" +
		"2026-08-07,25.50,吃\n" +
		"2026-08-06,abc,吃\n" + // unparseable amount
		"bad-date,10,吃\n" + // unparseable date
		"2026-08-05,7.00,行\n")

	// Strict mode stops at the first bad row and names it.
	_, err := Parse(raw, Options{})
	if err == nil {
		t.Fatal("strict mode should fail on the bad amount")
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Errorf("error should name row 3, got %q", err)
	}

	result, err := Parse(raw, Options{SkipInvalid: true})
	if err != nil {
		t.Fatalf("tolerant mode: %v", err)
	}
	if len(result.Records) != 2 {
		t.Errorf("got %d good records, want 2", len(result.Records))
	}
	if len(result.Errors) != 2 {
		t.Fatalf("got %d errors, want 2", len(result.Errors))
	}
	if result.Errors[0].Row != 3 || result.Errors[1].Row != 4 {
		t.Errorf("error rows = %d and %d, want 3 and 4", result.Errors[0].Row, result.Errors[1].Row)
	}
	// The raw row is kept so the caller can show what was skipped.
	if len(result.Errors[0].Raw) == 0 {
		t.Error("a skipped row should retain its raw values")
	}
}

func TestBlankRowsAreSkippedNotFailed(t *testing.T) {
	raw := []byte("date,amount,category\n2026-08-07,10,吃\n\n,,\n2026-08-06,20,行\n")
	result, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("blank rows should not be an error: %v", err)
	}
	if len(result.Records) != 2 {
		t.Errorf("got %d records, want 2", len(result.Records))
	}
	// csv.Reader drops wholly empty lines itself, so only the ",," row reaches us.
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestTypeVariants(t *testing.T) {
	cases := map[string]string{
		"expense": "expense", "支出": "expense", "消费": "expense", "out": "expense",
		"income": "income", "收入": "income", "收款": "income", "in": "income",
	}
	for input, want := range cases {
		raw := []byte("amount,category,type\n10,吃," + input + "\n")
		result, err := Parse(raw, Options{})
		if err != nil {
			t.Errorf("type %q: %v", input, err)
			continue
		}
		if got := result.Records[0].Type; got != want {
			t.Errorf("type %q -> %q, want %q", input, got, want)
		}
	}

	// An unrecognised direction must not be silently treated as an expense.
	if _, err := Parse([]byte("amount,category,type\n10,吃,不计收支\n"), Options{}); err == nil {
		t.Error("an unknown type should be rejected in strict mode")
	}
}

func TestDefaultTypeApplies(t *testing.T) {
	result, err := Parse([]byte("amount,category\n5000,工资\n"), Options{DefaultType: "income"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.Records[0].Type != "income" {
		t.Errorf("type = %q, want income", result.Records[0].Type)
	}
}

func TestSkipRowsBoundaries(t *testing.T) {
	raw := []byte("preamble\ndate,amount,category\n2026-08-07,10,吃\n")
	result, err := Parse(raw, Options{SkipRows: 1})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(result.Records))
	}
	// Row numbers must still refer to the original file, preamble included.
	if result.Records[0].Row != 3 {
		t.Errorf("row = %d, want 3", result.Records[0].Row)
	}

	if _, err := Parse(raw, Options{SkipRows: 99}); err == nil {
		t.Error("skipping past the end of the file should be an error")
	}
}

func TestSemicolonDelimiter(t *testing.T) {
	result, err := Parse([]byte("date;amount;category\n2026-08-07;25.50;吃\n"), Options{Delimiter: ';'})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Amount != "25.5" {
		t.Errorf("semicolon file produced %+v", result.Records)
	}
}

func TestKeepZeroAmount(t *testing.T) {
	raw := []byte("amount,category\n0.00,吃\n")

	result, err := Parse(raw, Options{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(result.Records) != 0 || result.Skipped != 1 {
		t.Errorf("zero amounts should be dropped by default, got %+v", result)
	}

	// Kept, the server will reject it — which is the point of the flag being opt-in.
	result, err = Parse(raw, Options{KeepZeroAmount: true})
	if err != nil {
		t.Fatalf("parse with KeepZeroAmount: %v", err)
	}
	if len(result.Records) != 1 {
		t.Errorf("KeepZeroAmount should retain the row, got %+v", result.Records)
	}
}
