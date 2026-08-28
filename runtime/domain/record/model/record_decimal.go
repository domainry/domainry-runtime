package recordmodel

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

const (
	RecordDecimalDefaultPrecision    = 19
	RecordDecimalDefaultScale        = 2
	RecordDecimalDefaultRoundingMode = "half_even"
	RecordDecimalDefaultCurrencyCode = "XXX"
)

type RecordDecimalConfig struct {
	Precision    int
	Scale        int32
	RoundingMode string
	CurrencyCode string
}

type RecordDecimalError struct {
	Code string
}

func (e *RecordDecimalError) Error() string {
	return e.Code
}

func RecordNormalizeDecimalConfig(raw map[string]any) (RecordDecimalConfig, error) {
	config := RecordDecimalConfig{
		Precision: RecordDecimalDefaultPrecision, Scale: RecordDecimalDefaultScale,
		RoundingMode: RecordDecimalDefaultRoundingMode, CurrencyCode: RecordDecimalDefaultCurrencyCode,
	}
	if raw == nil {
		return config, nil
	}
	var ok bool
	if value, exists := raw["precision"]; exists {
		config.Precision, ok = recordDecimalInteger(value)
		if !ok {
			return RecordDecimalConfig{}, recordDecimalError("backend.decimal.precision_invalid")
		}
	}
	if value, exists := raw["scale"]; exists {
		scale, valid := recordDecimalInteger(value)
		if !valid {
			return RecordDecimalConfig{}, recordDecimalError("backend.decimal.scale_invalid")
		}
		config.Scale = int32(scale)
	}
	if value, exists := raw["rounding_mode"]; exists {
		config.RoundingMode = strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	}
	if value, exists := raw["currency_code"]; exists {
		config.CurrencyCode = strings.ToUpper(strings.TrimSpace(fmt.Sprint(value)))
	}
	if config.Precision < 1 || config.Precision > 38 {
		return RecordDecimalConfig{}, recordDecimalError("backend.decimal.precision_invalid")
	}
	if config.Scale < 0 || int(config.Scale) > config.Precision {
		return RecordDecimalConfig{}, recordDecimalError("backend.decimal.scale_invalid")
	}
	if !recordDecimalRoundingModes()[config.RoundingMode] {
		return RecordDecimalConfig{}, recordDecimalError("backend.decimal.rounding_mode_invalid")
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(config.CurrencyCode) {
		return RecordDecimalConfig{}, recordDecimalError("backend.decimal.currency_code_invalid")
	}
	return config, nil
}

func RecordNormalizeDecimal(value any, config RecordDecimalConfig) (string, error) {
	text, ok := recordDecimalInput(value)
	if !ok {
		return "", recordDecimalError("backend.decimal.value_invalid")
	}
	parsed, err := decimal.NewFromString(text)
	if err != nil {
		return "", recordDecimalError("backend.decimal.value_invalid")
	}
	rounded := recordRoundDecimal(parsed, config.Scale, config.RoundingMode)
	minorUnits := rounded.Shift(config.Scale).BigInt()
	digits := len(new(big.Int).Abs(minorUnits).String())
	if digits > config.Precision {
		return "", recordDecimalError("backend.decimal.precision_overflow")
	}
	return rounded.StringFixed(config.Scale), nil
}

// RecordEncodeSQLiteDecimal stores an exact decimal as a fixed-width, ordered
// text value. The precision offset keeps negative and positive values sortable
// while avoiding SQLite REAL affinity entirely.
func RecordEncodeSQLiteDecimal(value any, config RecordDecimalConfig) (string, error) {
	normalized, err := RecordNormalizeDecimal(value, config)
	if err != nil {
		return "", err
	}
	parsed, _ := decimal.NewFromString(normalized)
	minorUnits := parsed.Shift(config.Scale).BigInt()
	offset := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(config.Precision)), nil)
	encoded := new(big.Int).Add(offset, minorUnits).String()
	if len(encoded) > config.Precision+1 {
		return "", recordDecimalError("backend.decimal.precision_overflow")
	}
	return strings.Repeat("0", config.Precision+1-len(encoded)) + encoded, nil
}

// RecordDecodeSQLiteDecimal reverses RecordEncodeSQLiteDecimal without a
// floating-point round trip.
func RecordDecodeSQLiteDecimal(value any, config RecordDecimalConfig) (string, error) {
	encoded := strings.TrimSpace(fmt.Sprint(value))
	if len(encoded) != config.Precision+1 {
		return "", recordDecimalError("backend.decimal.value_invalid")
	}
	stored, ok := new(big.Int).SetString(encoded, 10)
	if !ok {
		return "", recordDecimalError("backend.decimal.value_invalid")
	}
	offset := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(config.Precision)), nil)
	minorUnits := new(big.Int).Sub(stored, offset)
	return decimal.NewFromBigInt(minorUnits, -config.Scale).StringFixed(config.Scale), nil
}

func RecordAddDecimals(left, right string, config RecordDecimalConfig) (string, error) {
	return recordCalculateDecimal(left, right, config, "add")
}

func RecordSubtractDecimals(left, right string, config RecordDecimalConfig) (string, error) {
	return recordCalculateDecimal(left, right, config, "subtract")
}

// RecordCompareDecimals compares two exact decimal values after applying the
// published field precision, scale, and rounding contract.
func RecordCompareDecimals(left, right any, config RecordDecimalConfig) (int, error) {
	leftValue, leftErr := RecordNormalizeDecimal(left, config)
	rightValue, rightErr := RecordNormalizeDecimal(right, config)
	if leftErr != nil {
		return 0, leftErr
	}
	if rightErr != nil {
		return 0, rightErr
	}
	leftDecimal, _ := decimal.NewFromString(leftValue)
	rightDecimal, _ := decimal.NewFromString(rightValue)
	return leftDecimal.Cmp(rightDecimal), nil
}

func RecordMultiplyDecimals(left, right string, config RecordDecimalConfig) (string, error) {
	return recordCalculateDecimal(left, right, config, "multiply")
}

func RecordDivideDecimals(left, right string, config RecordDecimalConfig) (string, error) {
	return recordCalculateDecimal(left, right, config, "divide")
}

func RecordPercentageDecimal(left, right string, config RecordDecimalConfig) (string, error) {
	return recordCalculateDecimal(left, right, config, "percentage")
}

func recordCalculateDecimal(left, right string, config RecordDecimalConfig, operation string) (string, error) {
	leftValue, leftErr := decimal.NewFromString(strings.TrimSpace(left))
	rightValue, rightErr := decimal.NewFromString(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return "", recordDecimalError("backend.decimal.value_invalid")
	}
	var result decimal.Decimal
	switch operation {
	case "add":
		result = leftValue.Add(rightValue)
	case "subtract":
		result = leftValue.Sub(rightValue)
	case "multiply":
		result = leftValue.Mul(rightValue)
	case "divide":
		if rightValue.IsZero() {
			return "", recordDecimalError("backend.decimal.divide_by_zero")
		}
		result = leftValue.DivRound(rightValue, config.Scale+8)
	case "percentage":
		result = leftValue.Mul(rightValue).Div(decimal.NewFromInt(100))
	default:
		return "", recordDecimalError("backend.decimal.operator_invalid")
	}
	return RecordNormalizeDecimal(result.String(), config)
}

func recordRoundDecimal(value decimal.Decimal, scale int32, mode string) decimal.Decimal {
	switch mode {
	case "half_up":
		return value.Round(scale)
	case "down":
		return value.Truncate(scale)
	case "up":
		return value.RoundUp(scale)
	case "floor":
		return value.RoundFloor(scale)
	case "ceiling":
		return value.RoundCeil(scale)
	default:
		return value.RoundBank(scale)
	}
}

func recordDecimalInput(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		return text, text != ""
	case json.Number:
		return typed.String(), true
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	default:
		return "", false
	}
}

func recordDecimalInteger(value any) (int, bool) {
	if number, ok := value.(float64); ok {
		return int(number), !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
	}
	if number, ok := value.(float32); ok {
		converted := float64(number)
		return int(number), !math.IsNaN(converted) && !math.IsInf(converted, 0) && math.Trunc(converted) == converted
	}
	text, ok := recordDecimalInput(value)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.Atoi(text)
	return parsed, err == nil
}

func recordDecimalRoundingModes() map[string]bool {
	return map[string]bool{"half_even": true, "half_up": true, "down": true, "up": true, "floor": true, "ceiling": true}
}

func recordDecimalError(code string) error {
	return &RecordDecimalError{Code: code}
}
