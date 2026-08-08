package cmd

import (
	"fmt"
	"os"
	"strings"

	"sumi/internal/csvimport"

	"github.com/spf13/cobra"
)

// billImportCmd converts a CSV locally and sends it as one atomic batch. Parsing
// happens here rather than server-side because the file, its encoding and its
// column quirks are all local concerns; the server only ever sees clean JSON.
//
// The intended division of labour with an agent: the agent reads only the header
// (e.g. `head -20 file.csv`) and works out --map / --category-map / --skip-rows,
// then this command converts every row exactly and inserts them in one
// transaction. The agent never has to read the data rows, so it cannot mistype an
// amount and the import cannot land half-applied.
var billImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import transactions from a CSV file in one atomic batch",
	Long: `Import transactions from a CSV file.

All rows are inserted in a single transaction: if any row is rejected, nothing is
imported. Use --dry-run first to see what would be sent.

Columns are located with --map, which accepts either a header name or a 1-based
column number:

  sumi bill import --file bill.csv \
    --map date=交易时间,amount=金额,note=商品说明,type=收/支,category=交易分类 \
    --category-map "餐饮美食=吃,交通出行=行" \
    --skip-rows 3 --dry-run

Without --map, columns named date/amount/category/type/note/currency are used.
GBK files and a UTF-8 BOM are handled automatically; rows with a zero amount are
skipped, as wallet exports use those for non-monetary entries.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("--file is required")
		}

		raw, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", file, err)
		}

		mapping, err := parseKeyValueFlag(cmd, "map")
		if err != nil {
			return err
		}
		for field := range mapping {
			switch field {
			case csvimport.FieldDate, csvimport.FieldAmount, csvimport.FieldCategory,
				csvimport.FieldType, csvimport.FieldNote, csvimport.FieldCurrency:
			default:
				return fmt.Errorf("--map has unknown field %q: use date, amount, category, type, note or currency", field)
			}
		}
		categoryMap, err := parseKeyValueFlag(cmd, "category-map")
		if err != nil {
			return err
		}

		skipRows, _ := cmd.Flags().GetInt("skip-rows")
		encoding, _ := cmd.Flags().GetString("encoding")
		delimiter, _ := cmd.Flags().GetString("delimiter")
		defaultCategory, _ := cmd.Flags().GetString("default-category")
		defaultType, _ := cmd.Flags().GetString("default-type")
		skipInvalid, _ := cmd.Flags().GetBool("skip-invalid")
		keepZero, _ := cmd.Flags().GetBool("keep-zero-amount")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		var comma rune
		if trimmed := strings.TrimSpace(delimiter); trimmed != "" {
			runes := []rune(trimmed)
			if len(runes) != 1 {
				return fmt.Errorf("--delimiter must be a single character, got %q", delimiter)
			}
			comma = runes[0]
		}
		if defaultType != "" {
			if _, err := parseBillType(defaultType); err != nil {
				return fmt.Errorf("--default-type: %w", err)
			}
		}

		result, err := csvimport.Parse(raw, csvimport.Options{
			Mapping:         mapping,
			CategoryMap:     categoryMap,
			DefaultCategory: strings.TrimSpace(defaultCategory),
			DefaultType:     strings.TrimSpace(defaultType),
			SkipRows:        skipRows,
			Delimiter:       comma,
			Encoding:        encoding,
			SkipInvalid:     skipInvalid,
			KeepZeroAmount:  keepZero,
		})
		if err != nil {
			return err
		}
		if len(result.Records) == 0 {
			return fmt.Errorf("no importable rows found (%d skipped, %d invalid)", result.Skipped, len(result.Errors))
		}

		payloads := make([]billPayload, 0, len(result.Records))
		for _, record := range result.Records {
			billType, err := parseBillType(record.Type)
			if err != nil {
				return fmt.Errorf("row %d: %w", record.Row, err)
			}
			payloads = append(payloads, billPayload{
				Type:         billType,
				Amount:       record.Amount,
				Currency:     record.Currency,
				CategoryName: record.Category,
				Description:  record.Note,
				OccurredAt:   record.Date,
			})
		}

		skipped := make([]map[string]any, 0, len(result.Errors))
		for _, rowErr := range result.Errors {
			skipped = append(skipped, map[string]any{"row": rowErr.Row, "reason": rowErr.Reason})
		}

		if dryRun {
			// Show enough to confirm the mapping is right without dumping the file.
			preview := result.Records
			if len(preview) > 5 {
				preview = preview[:5]
			}
			return emitValue(map[string]any{
				"dry_run":       true,
				"file":          file,
				"header":        result.Header,
				"would_import":  len(payloads),
				"skipped_rows":  result.Skipped,
				"invalid_rows":  skipped,
				"preview":       preview,
				"preview_note":  "first 5 converted rows; re-run without --dry-run to import",
				"batch_maximum": maxImportRows,
			})
		}

		if len(payloads) > maxImportRows {
			return fmt.Errorf("%d rows exceeds the %d-row limit of one atomic batch; split the file",
				len(payloads), maxImportRows)
		}

		client, err := newAPIClient()
		if err != nil {
			return err
		}
		rawResponse, err := client.do("POST", "/api/transactions/batch", nil, map[string]any{"items": payloads})
		if err != nil {
			return err
		}

		var created []billRecord
		if err := unmarshalSession(rawResponse, &created); err != nil {
			return err
		}
		return emitValue(map[string]any{
			"imported":     len(created),
			"skipped_rows": result.Skipped,
			"invalid_rows": skipped,
			"first_id":     firstBillID(created),
			"last_id":      lastBillID(created),
		})
	},
}

// maxImportRows mirrors domain.MaxBatchSize. It is duplicated rather than imported
// so the CLI does not link the server's domain package; the server enforces the
// real limit either way.
const maxImportRows = 1000

// parseKeyValueFlag reads a repeatable "k=v,k=v" flag. Values may contain "=" so
// only the first one separates key from value.
func parseKeyValueFlag(cmd *cobra.Command, name string) (map[string]string, error) {
	entries, _ := cmd.Flags().GetStringSlice(name)
	if len(entries) == 0 {
		return nil, nil
	}

	parsed := make(map[string]string, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("--%s expects key=value pairs, got %q", name, entry)
		}
		parsed[key] = value
	}
	return parsed, nil
}

func firstBillID(bills []billRecord) int64 {
	if len(bills) == 0 {
		return 0
	}
	return bills[0].ID
}

func lastBillID(bills []billRecord) int64 {
	if len(bills) == 0 {
		return 0
	}
	return bills[len(bills)-1].ID
}

func init() {
	billImportCmd.Flags().String("file", "", "Path to the CSV file (required)")
	billImportCmd.Flags().StringSlice("map", nil,
		"Column mapping, e.g. date=交易时间,amount=金额 (header name or 1-based number)")
	billImportCmd.Flags().StringSlice("category-map", nil,
		"Rename source categories, e.g. 餐饮美食=吃,交通出行=行")
	billImportCmd.Flags().String("default-category", "", "Category for rows that have none")
	billImportCmd.Flags().String("default-type", "expense", "expense or income, when no type column is mapped")
	billImportCmd.Flags().Int("skip-rows", 0, "Lines to drop before the header row")
	billImportCmd.Flags().String("encoding", "", "utf8 or gbk (default: auto-detect)")
	billImportCmd.Flags().String("delimiter", "", "Field delimiter (default: comma)")
	billImportCmd.Flags().Bool("skip-invalid", false, "Skip unconvertible rows instead of aborting the file")
	billImportCmd.Flags().Bool("keep-zero-amount", false, "Keep rows whose amount is zero (dropped by default)")
	billImportCmd.Flags().Bool("dry-run", false, "Show what would be imported without writing anything")

	billCmd.AddCommand(billImportCmd)
}
