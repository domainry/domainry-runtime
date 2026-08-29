// Package storage defines the physical schema strategy required by the
// Metadata persistence owner. Implementations live in sibling engine packages.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ReadColumns(rows *sql.Rows, err error, table string) (map[string]bool, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return result, rows.Err()
}

func ReadColumnTypes(rows *sql.Rows, err error, table string) (map[string]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, dataType string
		var precision, scale sql.NullInt64
		if err := rows.Scan(&name, &dataType, &precision, &scale); err != nil {
			return nil, err
		}
		if precision.Valid && scale.Valid && (strings.EqualFold(dataType, "decimal") || strings.EqualFold(dataType, "numeric")) {
			dataType = fmt.Sprintf("%s(%d,%d)", dataType, precision.Int64, scale.Int64)
		}
		result[name] = dataType
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return result, rows.Err()
}

func ReadIndexes(rows *sql.Rows, err error) (map[string]bool, error) {
	result := map[string]bool{}
	if err != nil {
		return result, nil
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Profile interface {
	IDColumnType() string
	FieldColumnType(definitionmodel.FieldSchema, bool) string
	Columns(context.Context, Queryer, ormbuilder.Renderer, string, string) (map[string]bool, error)
	ColumnTypes(context.Context, Queryer, ormbuilder.Renderer, string, string) (map[string]string, error)
	Indexes(context.Context, Queryer, ormbuilder.Renderer, string, string) (map[string]bool, error)
	DropIndex(context.Context, Executor, ormbuilder.Renderer, string, string) error
}

func DecimalConfig(field definitionmodel.FieldSchema) recordmodel.RecordDecimalConfig {
	config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
	if err == nil {
		return config
	}
	config, _ = recordmodel.RecordNormalizeDecimalConfig(nil)
	return config
}

func IndexedTextLength(field definitionmodel.FieldSchema) int {
	const maximum = 191
	value := 0
	switch typed := field.Config["max_length"].(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		value = int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		value = int(parsed)
	}
	if value > 0 && value < maximum {
		return value
	}
	return maximum
}
