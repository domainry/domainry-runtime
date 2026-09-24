package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type QueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
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

// PhysicalSchemaSnapshot contains the physical columns and indexes for a set
// of application tables. A missing ColumnsByTable entry means the table does
// not exist; an empty IndexesByTable entry means the table has no indexes.
type PhysicalSchemaSnapshot struct {
	ColumnsByTable map[string]map[string]string
	IndexesByTable map[string]map[string]bool
}

type ColumnDefinition struct {
	Name string
	Type string
}

// BulkPhysicalSchemaInspector is an optional storage profile capability used
// by upgrade planning. Profiles that implement it can avoid one metadata query
// per table when the database is separated from Runtime by a high-latency
// network connection.
type BulkPhysicalSchemaInspector interface {
	PhysicalSchema(context.Context, Queryer, query.Renderer, string, []string) (PhysicalSchemaSnapshot, error)
}

// BatchColumnAdder is an optional capability for dialects that can add
// multiple columns with one ALTER TABLE statement.
type BatchColumnAdder interface {
	BatchAddColumnsSQL(query.Renderer, string, []ColumnDefinition) string
}

type Profile interface {
	IDColumnType() string
	FieldColumnType(definitionmodel.FieldSchema, bool) string
	Columns(context.Context, Queryer, query.Renderer, string, string) (map[string]bool, error)
	ColumnTypes(context.Context, Queryer, query.Renderer, string, string) (map[string]string, error)
	Indexes(context.Context, Queryer, query.Renderer, string, string) (map[string]bool, error)
	DropIndex(context.Context, Executor, query.Renderer, string, string) error
	ConditionalUniquePlan(query.Renderer, string, string, recordvalidation.RecordConditionalUniquePolicy) ConditionalUniquePlan
	ExactDecimalUpgradeAllowed(string, definitionmodel.FieldSchema) bool
}

func NormalizePhysicalType(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
}

type ConditionalUniquePlan struct {
	GuardColumn       string
	AddGuardStatement string
	PartialStatement  string
	IndexFields       []string
}

func GuardColumn(indexName string) string {
	hash := sha256.Sum256([]byte(indexName))
	return "_domainry_cuq_" + hex.EncodeToString(hash[:])[:16]
}

func ConditionalUniqueFields(policy recordvalidation.RecordConditionalUniquePolicy) []string {
	return append([]string{"workspace_id"}, policy.Fields...)
}

func ConditionalUniqueCondition(renderer query.Renderer, policy recordvalidation.RecordConditionalUniquePolicy) string {
	values := make([]string, len(policy.ConditionValues))
	for index, value := range policy.ConditionValues {
		values[index] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return renderer.Identifier(policy.ConditionField) + " IN (" + strings.Join(values, ", ") + ")"
}

func PartialConditionalUniquePlan(renderer query.Renderer, table, indexName string, policy recordvalidation.RecordConditionalUniquePolicy) ConditionalUniquePlan {
	fields := ConditionalUniqueFields(policy)
	quoted := make([]string, len(fields))
	for index, field := range fields {
		quoted[index] = renderer.Identifier(field)
	}
	return ConditionalUniquePlan{IndexFields: fields, PartialStatement: "CREATE UNIQUE INDEX IF NOT EXISTS " + renderer.Identifier(indexName) + " ON " + renderer.Table(table) + " (" + strings.Join(quoted, ", ") + ") WHERE " + ConditionalUniqueCondition(renderer, policy)}
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
