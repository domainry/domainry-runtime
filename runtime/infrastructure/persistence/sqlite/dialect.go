package sqlite

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/shopspring/decimal"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"

	modernsqlite "modernc.org/sqlite"
)

func init() {
	if err := modernsqlite.RegisterDeterministicScalarFunction("runtime_decimal_minor", 2, sqliteDecimalMinor); err != nil {
		panic(err)
	}
	if err := modernsqlite.RegisterDeterministicScalarFunction("runtime_currency_divide_minor", 2, sqliteCurrencyDivideMinor); err != nil {
		panic(err)
	}
	if err := modernsqlite.RegisterDeterministicScalarFunction("runtime_exact_decimal_encode", 4, sqliteExactDecimalEncode); err != nil {
		panic(err)
	}
	for name, average := range map[string]bool{"runtime_decimal_sum_minor": false, "runtime_decimal_avg_minor": true} {
		average := average
		if err := modernsqlite.RegisterFunction(name, &modernsqlite.FunctionImpl{NArgs: 1, Deterministic: true, MakeAggregate: func(modernsqlite.FunctionContext) (modernsqlite.AggregateFunction, error) {
			return &sqliteDecimalAggregate{average: average, sum: new(big.Int)}, nil
		}}); err != nil {
			panic(err)
		}
	}
}

func sqliteExactDecimalEncode(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 4 || args[0] == nil {
		return nil, nil
	}
	precision, precisionOK := args[1].(int64)
	scale, scaleOK := args[2].(int64)
	if !precisionOK || !scaleOK {
		return nil, fmt.Errorf("invalid exact decimal migration config")
	}
	config, err := recordmodel.RecordNormalizeDecimalConfig(map[string]any{
		"precision":     precision,
		"scale":         scale,
		"rounding_mode": strings.TrimSpace(fmt.Sprint(args[3])),
	})
	if err != nil {
		return nil, err
	}
	source := strings.TrimSpace(fmt.Sprint(args[0]))
	normalized, err := recordmodel.RecordNormalizeDecimal(source, config)
	if err != nil {
		return nil, err
	}
	sourceDecimal, sourceErr := decimal.NewFromString(source)
	normalizedDecimal, normalizedErr := decimal.NewFromString(normalized)
	if sourceErr != nil || normalizedErr != nil || !sourceDecimal.Equal(normalizedDecimal) {
		return nil, fmt.Errorf("legacy decimal value exceeds declared scale")
	}
	return recordmodel.RecordEncodeSQLiteDecimal(normalized, config)
}

func sqliteCurrencyDivideMinor(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 2 || args[0] == nil || args[1] == nil {
		return nil, nil
	}
	minor, ok := sqliteAggregateInteger(args[0])
	if !ok {
		return nil, fmt.Errorf("currency dividend requires integer minor units")
	}
	divisor, err := decimal.NewFromString(strings.TrimSpace(fmt.Sprint(args[1])))
	if err != nil {
		return nil, fmt.Errorf("invalid currency divisor")
	}
	if divisor.IsZero() {
		return nil, fmt.Errorf("currency divisor is zero; use NULLIF or CASE")
	}
	result := decimal.NewFromBigInt(minor, 0).Div(divisor).Round(0).BigInt()
	return result.String(), nil
}

func sqliteDecimalMinor(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 2 || args[0] == nil {
		return nil, nil
	}
	encoded := strings.TrimSpace(fmt.Sprint(args[0]))
	precision, ok := args[1].(int64)
	if !ok || precision <= 0 || precision > 38 {
		return nil, fmt.Errorf("invalid decimal precision")
	}
	stored, ok := new(big.Int).SetString(encoded, 10)
	if !ok {
		return nil, fmt.Errorf("invalid encoded decimal")
	}
	offset := new(big.Int).Exp(big.NewInt(10), big.NewInt(precision), nil)
	minor := new(big.Int).Sub(stored, offset)
	if !minor.IsInt64() {
		return nil, fmt.Errorf("decimal minor units exceed int64")
	}
	return minor.Int64(), nil
}

type sqliteDecimalAggregate struct {
	average bool
	sum     *big.Int
	count   int64
}

func (a *sqliteDecimalAggregate) Step(_ *modernsqlite.FunctionContext, values []sqldriver.Value) error {
	if len(values) != 1 || values[0] == nil {
		return nil
	}
	value, ok := sqliteAggregateInteger(values[0])
	if !ok {
		return fmt.Errorf("decimal aggregate requires integer minor units")
	}
	a.sum.Add(a.sum, value)
	a.count++
	return nil
}
func (a *sqliteDecimalAggregate) WindowInverse(_ *modernsqlite.FunctionContext, values []sqldriver.Value) error {
	if len(values) != 1 || values[0] == nil {
		return nil
	}
	value, ok := sqliteAggregateInteger(values[0])
	if !ok {
		return fmt.Errorf("decimal aggregate requires integer minor units")
	}
	a.sum.Sub(a.sum, value)
	a.count--
	return nil
}
func (a *sqliteDecimalAggregate) WindowValue(_ *modernsqlite.FunctionContext) (sqldriver.Value, error) {
	if a.count == 0 {
		return nil, nil
	}
	if !a.average {
		return a.sum.String(), nil
	}
	divisor := big.NewInt(a.count)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(a.sum, divisor, remainder)
	absRemainder := new(big.Int).Abs(remainder)
	if absRemainder.Mul(absRemainder, big.NewInt(2)).Cmp(divisor) >= 0 {
		if a.sum.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return quotient.String(), nil
}
func (a *sqliteDecimalAggregate) Final(*modernsqlite.FunctionContext) {}

func sqliteAggregateInteger(value sqldriver.Value) (*big.Int, bool) {
	switch typed := value.(type) {
	case int64:
		return big.NewInt(typed), true
	case string:
		parsed, ok := new(big.Int).SetString(strings.TrimSpace(typed), 10)
		return parsed, ok
	case []byte:
		parsed, ok := new(big.Int).SetString(strings.TrimSpace(string(typed)), 10)
		return parsed, ok
	}
	return nil, false
}

type Dialect struct{}

func (Dialect) Name() string { return "sqlite" }

func (Dialect) SQLDriver() string { return "sqlite" }

func (Dialect) DSN(cfg config.Config) (string, error) {
	if strings.TrimSpace(cfg.DatabaseDSN) != "" {
		return strings.TrimSpace(cfg.DatabaseDSN), nil
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		return "../data/app.db", nil
	}
	return strings.TrimSpace(cfg.DBPath), nil
}

func (Dialect) Configure(ctx context.Context, db *sql.DB, dsn string) error {
	if dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return fmt.Errorf("create sqlite database directory: %w", err)
		}
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("configure sqlite database: %w", err)
	}
	return nil
}

func (Dialect) Identifier(value string) string {
	return database.QuoteIdentifier(value, `"`)
}

func (Dialect) Placeholder(position int) string {
	return database.QuestionPlaceholder(position)
}

func (Dialect) SchemaMigrationSQL() string {
	return `CREATE TABLE IF NOT EXISTS "_schema_migrations" ("path" TEXT PRIMARY KEY, "applied_at" TEXT NOT NULL)`
}
