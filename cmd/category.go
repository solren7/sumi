package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type categoryNode struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Type     int16          `json:"type"`
	Level    int16          `json:"level"`
	Children []categoryNode `json:"children"`
}

var categoryCmd = groupCommand("category", "Inspect and create categories")

var categoryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the category tree",
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

		query := url.Values{}
		query.Set("type", strconv.Itoa(int(billType)))
		raw, err := client.do("GET", "/api/categories/", query, nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

var categoryAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Create a second-level category under an existing top-level one",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		parent, _ := cmd.Flags().GetString("parent")
		rawType, _ := cmd.Flags().GetString("type")

		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("--name is required")
		}
		if strings.TrimSpace(parent) == "" {
			return fmt.Errorf("--parent is required: a top-level category name, list them with `sumi category list`")
		}
		billType, err := parseBillType(rawType)
		if err != nil {
			return err
		}

		raw, err := client.do("POST", "/api/categories/", nil, map[string]any{
			"name":        strings.TrimSpace(name),
			"type":        billType,
			"parent_name": strings.TrimSpace(parent),
		})
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

// resolveCategoryIDByName exists because listing transactions filters by
// category_id only, while callers work in names. billType 0 means "unknown", in
// which case both trees are searched and an ambiguous name is reported rather
// than guessed.
func resolveCategoryIDByName(client *apiClient, name string, billType int16) (int64, error) {
	types := []int16{1, 2}
	if billType != 0 {
		types = []int16{billType}
	}

	matches := make([]categoryNode, 0, 2)
	for _, categoryType := range types {
		query := url.Values{}
		query.Set("type", strconv.Itoa(int(categoryType)))
		raw, err := client.do("GET", "/api/categories/", query, nil)
		if err != nil {
			return 0, err
		}

		var tree []categoryNode
		if err := json.Unmarshal(raw, &tree); err != nil {
			return 0, fmt.Errorf("cannot parse category tree: %w", err)
		}
		for _, parent := range tree {
			for _, child := range parent.Children {
				if child.Level == 2 && strings.EqualFold(strings.TrimSpace(child.Name), name) {
					matches = append(matches, child)
				}
			}
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("category %q not found; list valid names with `sumi category list`", name)
	case 1:
		return matches[0].ID, nil
	default:
		return 0, fmt.Errorf("category %q exists for both expense and income; add --type to disambiguate", name)
	}
}

func init() {
	categoryListCmd.Flags().String("type", "expense", "expense or income")

	categoryAddCmd.Flags().String("name", "", "New category name (required)")
	categoryAddCmd.Flags().String("parent", "", "Existing top-level category name (required)")
	categoryAddCmd.Flags().String("type", "expense", "expense or income")

	categoryCmd.AddCommand(categoryListCmd, categoryAddCmd)
	rootView.AddCommand(categoryCmd)
}
