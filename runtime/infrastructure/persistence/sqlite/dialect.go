package sqlite

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"math/big"
	"strings"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/shopspring/decimal"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
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
	if err := modernsqlite.RegisterDeterministicScalarFunction("runtime_date_bucket", 3, sqliteDateBucket); err != nil {
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

func sqliteDateBucket(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 3 || args[0] == nil {
		return nil, nil
	}
	value, grain, zone := strings.TrimSpace(fmt.Sprint(args[0])), strings.TrimSpace(fmt.Sprint(args[1])), strings.TrimSpace(fmt.Sprint(args[2]))
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("invalid report timezone %q: %w", zone, err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("invalid report datetime %q: %w", value, err)
	}
	local := parsed.In(location)
	year, month, day := local.Date()
	switch grain {
	case "hour":
		local = time.Date(year, month, day, local.Hour(), 0, 0, 0, location)
	case "day":
		local = time.Date(year, month, day, 0, 0, 0, 0, location)
	case "week":
		weekday := (int(local.Weekday()) + 6) % 7
		local = time.Date(year, month, day-weekday, 0, 0, 0, 0, location)
	case "month":
		local = time.Date(year, month, 1, 0, 0, 0, 0, location)
	case "quarter":
		local = time.Date(year, time.Month((int(month)-1)/3*3+1), 1, 0, 0, 0, 0, location)
	case "year":
		local = time.Date(year, 1, 1, 0, 0, 0, 0, location)
	default:
		return nil, fmt.Errorf("invalid report date grain %q", grain)
	}
	return local.Format(time.RFC3339), nil
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
	connection, err := runtimeSQLiteConnectionConfig(cfg)
	if err != nil {
		return "", err
	}
	return connection.DSN()
}

func (Dialect) Configure(ctx context.Context, db *sql.DB, cfg config.Config) error {
	connection, err := runtimeSQLiteConnectionConfig(cfg)
	if err != nil {
		return err
	}
	return ormsqlite.InitializeOwned(ctx, db, connection)
}

func runtimeSQLiteConnectionConfig(cfg config.Config) (ormsqlite.OwnedConnectionConfig, error) {
	dataSource := strings.TrimSpace(cfg.DatabaseDSN)
	if dataSource == "" {
		dataSource = strings.TrimSpace(cfg.DBPath)
	}
	if dataSource == "" {
		dataSource = "../data/runtime.db"
	}
	normalized := strings.ToLower(dataSource)
	if normalized == ":memory:" || strings.Contains(normalized, "mode=memory") {
		return ormsqlite.OwnedConnectionConfig{}, fmt.Errorf("Runtime SQLite requires a file database")
	}
	connection := ormsqlite.DefaultOwnedConnectionConfig(dataSource)
	if cfg.DatabaseLockTimeout > 0 {
		connection.BusyTimeout = cfg.DatabaseLockTimeout
	}
	if cfg.DatabaseMaxOpenConns > 0 {
		connection.MaxOpenConnections = cfg.DatabaseMaxOpenConns
	}
	if cfg.DatabaseMaxIdleConns > 0 {
		connection.MaxIdleConnections = cfg.DatabaseMaxIdleConns
	}
	if connection.MaxIdleConnections > connection.MaxOpenConnections {
		connection.MaxIdleConnections = connection.MaxOpenConnections
	}
	return connection, nil
}

func (Dialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.SQLite)
	return value
}

func (Dialect) SchemaMigrationSQL() string {
	return `CREATE TABLE IF NOT EXISTS "_schema_migrations" ("path" TEXT PRIMARY KEY, "applied_at" TEXT NOT NULL)`
}
