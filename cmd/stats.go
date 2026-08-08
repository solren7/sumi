package cmd

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var statsCmd = groupCommand("stats", "Read spending summaries")

func monthQuery(cmd *cobra.Command) url.Values {
	query := url.Values{}
	if month, _ := cmd.Flags().GetString("month"); strings.TrimSpace(month) != "" {
		query.Set("month", strings.TrimSpace(month))
	}
	return query
}

var statsMonthlyCmd = &cobra.Command{
	Use:   "monthly",
	Short: "Income, expense and net total per currency for one month",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}
		raw, err := client.do("GET", "/api/stats/monthly", monthQuery(cmd), nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var statsDailyCmd = &cobra.Command{
	Use:   "daily",
	Short: "Per-day income and expense for one month",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}
		raw, err := client.do("GET", "/api/stats/daily", monthQuery(cmd), nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var statsCategoryCmd = &cobra.Command{
	Use:   "category",
	Short: "Per-category totals for one month",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		rawType, _ := cmd.Flags().GetString("type")
		billType, err := parseBillType(rawType)
		if err != nil {
			return err
		}

		query := monthQuery(cmd)
		query.Set("type", strconv.Itoa(int(billType)))
		raw, err := client.do("GET", "/api/stats/category", query, nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

func init() {
	statsMonthlyCmd.Flags().String("month", "", "Month as YYYY-MM (default: current month)")
	statsDailyCmd.Flags().String("month", "", "Month as YYYY-MM (default: current month)")
	statsCategoryCmd.Flags().String("month", "", "Month as YYYY-MM (default: current month)")
	statsCategoryCmd.Flags().String("type", "expense", "expense or income")

	statsCmd.AddCommand(statsMonthlyCmd, statsDailyCmd, statsCategoryCmd)
	rootView.AddCommand(statsCmd)
}
