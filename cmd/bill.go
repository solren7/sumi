package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type billRecord struct {
	ID          int64  `json:"id"`
	Type        int16  `json:"type"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	CategoryID  int64  `json:"category_id"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
}

// billPayload mirrors the API's transaction body. Currency is omitted when empty
// so the server can fall back to the user's default currency.
type billPayload struct {
	Type         int16  `json:"type"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency,omitempty"`
	CategoryID   int64  `json:"category_id,omitempty"`
	CategoryName string `json:"category_name,omitempty"`
	Description  string `json:"description,omitempty"`
	OccurredAt   string `json:"occurred_at,omitempty"`
}

// batchItem is the CLI-facing shape for `bill add-batch`. Field names match the
// flags of `bill add` so one mental model covers both.
type batchItem struct {
	Amount   string `json:"amount"`
	Category string `json:"category"`
	Type     string `json:"type"`
	Note     string `json:"note"`
	Date     string `json:"date"`
	Currency string `json:"currency"`
}

var billCmd = groupCommand("bill", "Query and record transactions")

var billAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Record one transaction",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		amount, _ := cmd.Flags().GetString("amount")
		category, _ := cmd.Flags().GetString("category")
		rawType, _ := cmd.Flags().GetString("type")
		date, _ := cmd.Flags().GetString("date")
		note, _ := cmd.Flags().GetString("note")
		currency, _ := cmd.Flags().GetString("currency")

		if strings.TrimSpace(amount) == "" {
			return fmt.Errorf("--amount is required")
		}
		if strings.TrimSpace(category) == "" {
			return fmt.Errorf("--category is required (a second-level category name, e.g. 吃)")
		}
		billType, err := parseBillType(rawType)
		if err != nil {
			return err
		}

		raw, err := client.do("POST", "/api/transactions/", nil, billPayload{
			Type:         billType,
			Amount:       strings.TrimSpace(amount),
			Currency:     strings.TrimSpace(currency),
			CategoryName: strings.TrimSpace(category),
			Description:  note,
			OccurredAt:   strings.TrimSpace(date),
		})
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var billAddBatchCmd = &cobra.Command{
	Use:   "add-batch",
	Short: "Record several transactions atomically (all or nothing)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		rawItems, _ := cmd.Flags().GetString("items")
		if strings.TrimSpace(rawItems) == "" {
			return fmt.Errorf("--items is required: a JSON array of {amount, category, type?, note?, date?, currency?}")
		}

		var items []batchItem
		if err := json.Unmarshal([]byte(rawItems), &items); err != nil {
			return fmt.Errorf("--items is not a valid JSON array: %w", err)
		}
		if len(items) == 0 {
			return fmt.Errorf("--items must contain at least one transaction")
		}

		payloads := make([]billPayload, 0, len(items))
		for i, item := range items {
			if strings.TrimSpace(item.Amount) == "" {
				return fmt.Errorf("items[%d]: amount is required", i)
			}
			if strings.TrimSpace(item.Category) == "" {
				return fmt.Errorf("items[%d]: category is required", i)
			}
			itemType := item.Type
			if strings.TrimSpace(itemType) == "" {
				itemType = "expense"
			}
			billType, err := parseBillType(itemType)
			if err != nil {
				return fmt.Errorf("items[%d]: %w", i, err)
			}
			payloads = append(payloads, billPayload{
				Type:         billType,
				Amount:       strings.TrimSpace(item.Amount),
				Currency:     strings.TrimSpace(item.Currency),
				CategoryName: strings.TrimSpace(item.Category),
				Description:  item.Note,
				OccurredAt:   strings.TrimSpace(item.Date),
			})
		}

		raw, err := client.do("POST", "/api/transactions/batch", nil, map[string]any{"items": payloads})
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var billListCmd = &cobra.Command{
	Use:   "list",
	Short: "List transactions, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		query := url.Values{}

		rawType, _ := cmd.Flags().GetString("type")
		var billType int16
		if strings.TrimSpace(rawType) != "" {
			billType, err = parseBillType(rawType)
			if err != nil {
				return err
			}
			query.Set("type", strconv.Itoa(int(billType)))
		}

		if category, _ := cmd.Flags().GetString("category"); strings.TrimSpace(category) != "" {
			categoryID, err := resolveCategoryIDByName(client, strings.TrimSpace(category), billType)
			if err != nil {
				return err
			}
			query.Set("category_id", strconv.FormatInt(categoryID, 10))
		}
		if keyword, _ := cmd.Flags().GetString("keyword"); strings.TrimSpace(keyword) != "" {
			query.Set("keyword", strings.TrimSpace(keyword))
		}
		if currency, _ := cmd.Flags().GetString("currency"); strings.TrimSpace(currency) != "" {
			query.Set("currency", strings.TrimSpace(currency))
		}
		if from, _ := cmd.Flags().GetString("from"); strings.TrimSpace(from) != "" {
			query.Set("start_time", strings.TrimSpace(from))
		}
		if to, _ := cmd.Flags().GetString("to"); strings.TrimSpace(to) != "" {
			query.Set("end_time", strings.TrimSpace(to))
		}
		limit, _ := cmd.Flags().GetInt("limit")
		query.Set("limit", strconv.Itoa(limit))
		offset, _ := cmd.Flags().GetInt("offset")
		query.Set("offset", strconv.Itoa(offset))

		raw, err := client.do("GET", "/api/transactions/", query, nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var billGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show one transaction",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}
		raw, err := client.do("GET", "/api/transactions/"+url.PathEscape(args[0]), nil, nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

// billUpdateCmd reads the current row first and resends every field, because the
// API's PUT replaces the whole transaction: sending only the changed flag would
// blank out description, currency and date.
var billUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Change fields of one transaction, leaving unspecified fields intact",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		existingRaw, err := client.do("GET", "/api/transactions/"+url.PathEscape(args[0]), nil, nil)
		if err != nil {
			return err
		}
		var existing billRecord
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return fmt.Errorf("cannot parse existing transaction: %w", err)
		}

		payload := billPayload{
			Type:        existing.Type,
			Amount:      existing.Amount,
			Currency:    existing.Currency,
			CategoryID:  existing.CategoryID,
			Description: existing.Description,
			OccurredAt:  existing.OccurredAt,
		}

		flags := cmd.Flags()
		if flags.Changed("type") {
			rawType, _ := flags.GetString("type")
			billType, err := parseBillType(rawType)
			if err != nil {
				return err
			}
			payload.Type = billType
		}
		if flags.Changed("amount") {
			amount, _ := flags.GetString("amount")
			payload.Amount = strings.TrimSpace(amount)
		}
		if flags.Changed("currency") {
			currency, _ := flags.GetString("currency")
			payload.Currency = strings.TrimSpace(currency)
		}
		if flags.Changed("category") {
			category, _ := flags.GetString("category")
			payload.CategoryID = 0
			payload.CategoryName = strings.TrimSpace(category)
		}
		if flags.Changed("note") {
			note, _ := flags.GetString("note")
			payload.Description = note
		}
		if flags.Changed("date") {
			date, _ := flags.GetString("date")
			payload.OccurredAt = strings.TrimSpace(date)
		}

		raw, err := client.do("PUT", "/api/transactions/"+url.PathEscape(args[0]), nil, payload)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var billDeleteCmd = &cobra.Command{
	Use:   "del <id>",
	Short: "Delete one transaction",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid transaction id %q", args[0])
		}
		if _, err := client.do("DELETE", "/api/transactions/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			return err
		}
		return emitValue(map[string]any{"deleted": true, "id": id})
	},
}

func init() {
	billAddCmd.Flags().String("amount", "", "Amount, e.g. 25.50 (required)")
	billAddCmd.Flags().String("category", "", "Second-level category name, e.g. 吃 (required)")
	billAddCmd.Flags().String("type", "expense", "expense or income")
	billAddCmd.Flags().String("date", "", "Date as YYYY-MM-DD (default: today)")
	billAddCmd.Flags().String("note", "", "Free-text description")
	billAddCmd.Flags().String("currency", "", "3-letter code (default: the user's default currency)")

	billAddBatchCmd.Flags().String("items", "", "JSON array of {amount, category, type?, note?, date?, currency?} (required)")

	billListCmd.Flags().String("type", "", "expense or income (default: both)")
	billListCmd.Flags().String("category", "", "Filter by second-level category name")
	billListCmd.Flags().String("keyword", "", "Case-insensitive substring match on the note")
	billListCmd.Flags().String("currency", "", "Filter by 3-letter currency code")
	billListCmd.Flags().String("from", "", "Only transactions on or after this date (inclusive)")
	billListCmd.Flags().String("to", "", "Only transactions before this date (exclusive)")
	billListCmd.Flags().Int("limit", 20, "Maximum rows to return")
	billListCmd.Flags().Int("offset", 0, "Rows to skip")

	billUpdateCmd.Flags().String("amount", "", "New amount")
	billUpdateCmd.Flags().String("category", "", "New second-level category name")
	billUpdateCmd.Flags().String("type", "", "expense or income")
	billUpdateCmd.Flags().String("date", "", "New date as YYYY-MM-DD")
	billUpdateCmd.Flags().String("note", "", "New description")
	billUpdateCmd.Flags().String("currency", "", "New 3-letter currency code")

	billCmd.AddCommand(billAddCmd, billAddBatchCmd, billListCmd, billGetCmd, billUpdateCmd, billDeleteCmd)
	rootView.AddCommand(billCmd)
}
