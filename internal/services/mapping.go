package services

// Mapping from sqlc rows to the domain types in internal/domain. This is the only
// place that knows both shapes: domain must not import dbgen, and handlers must not
// import it either.

import (
	"sumi/internal/domain"
	"sumi/internal/repository/dbgen"
)

func billFromRow(row dbgen.Bill) domain.Bill {
	return domain.Bill{
		ID:          row.ID,
		UserID:      row.UserID,
		Type:        row.Type,
		Amount:      row.Amount,
		Currency:    row.Currency,
		CategoryID:  row.CategoryID,
		Description: row.Description,
		OccurredAt:  row.OccurredAt,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func billsFromRows(rows []dbgen.Bill) []domain.Bill {
	bills := make([]domain.Bill, 0, len(rows))
	for _, row := range rows {
		bills = append(bills, billFromRow(row))
	}
	return bills
}

func apiKeyFromRow(row dbgen.ApiKey) domain.APIKey {
	key := domain.APIKey{
		ID:        row.ID,
		Name:      row.Name,
		Prefix:    row.KeyPrefix,
		Scopes:    row.Scopes,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
	}
	if row.LastUsedAt.Valid {
		value := row.LastUsedAt.Time
		key.LastUsedAt = &value
	}
	if row.ExpiresAt.Valid {
		value := row.ExpiresAt.Time
		key.ExpiresAt = &value
	}
	return key
}

func userFromRow(row dbgen.User) domain.User {
	return domain.User{
		ID:              row.ID,
		Email:           row.Email,
		Username:        row.Username,
		DefaultCurrency: row.DefaultCurrency,
		Timezone:        row.Timezone,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}
