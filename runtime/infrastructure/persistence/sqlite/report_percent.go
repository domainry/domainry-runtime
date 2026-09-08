package sqlite

import (
	sqldriver "database/sql/driver"
	"fmt"
	"math/big"

	modernsqlite "modernc.org/sqlite"
)

// Products can exceed int64 in scaled minor units even when their final whole
// currency units fit. Return integer text so SQLite never promotes them to REAL.
func sqliteDecimalMultiplyMinor(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("invalid exact decimal product")
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	left, leftOK := sqliteAggregateInteger(args[0])
	right, rightOK := sqliteAggregateInteger(args[1])
	if !leftOK || !rightOK {
		return nil, fmt.Errorf("exact decimal product requires integer minor units")
	}
	return new(big.Int).Mul(left, right).String(), nil
}

func sqliteDecimalFloorUnits(_ *modernsqlite.FunctionContext, args []sqldriver.Value) (sqldriver.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("invalid exact decimal floor")
	}
	if args[0] == nil {
		return nil, nil
	}
	minor, valid := sqliteAggregateInteger(args[0])
	scale, scaleOK := args[1].(int64)
	if !valid || !scaleOK || scale < 0 || scale > 38 {
		return nil, fmt.Errorf("invalid exact decimal floor units")
	}
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil)
	// Div with a positive divisor rounds toward negative infinity, including
	// negative correction amounts; Quo would incorrectly truncate toward zero.
	return new(big.Int).Div(minor, factor).String(), nil
}
