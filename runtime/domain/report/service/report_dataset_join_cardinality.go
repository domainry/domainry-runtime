package service

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func reportJoinRecordKey(record *recordmodel.Record, equalities []reportmodel.ReportDatasetJoinFieldEquality, left bool) (string, bool) {
	if record == nil || len(equalities) == 0 {
		return "", false
	}
	var key strings.Builder
	for _, equality := range equalities {
		field := equality.RightField
		if left {
			field = equality.LeftField
		}
		value, ok := reportRecordPointerValue(record, field)
		if !ok || value == nil {
			return "", false
		}
		stable := reportStableValue(value)
		fmt.Fprintf(&key, "%d:%s", len(stable), stable)
	}
	return key.String(), true
}

func reportValidateJoinCardinality(leftRows []reportDatasetRow, rightRecords []recordmodel.Record, join reportmodel.ReportDatasetJoin) error {
	cardinality := strings.TrimSpace(join.Cardinality)
	if cardinality == "one_to_many" {
		return nil
	}
	right := map[string]map[string]bool{}
	for index := range rightRecords {
		record := &rightRecords[index]
		if key, ok := reportJoinRecordKey(record, join.Equalities(), false); ok {
			if right[key] == nil {
				right[key] = map[string]bool{}
			}
			right[key][record.ID] = true
			if len(right[key]) > 1 {
				return reportJoinCardinalityError(join)
			}
		}
	}
	if cardinality != "one_to_one" {
		return nil
	}
	left := map[string]map[string]bool{}
	for _, row := range leftRows {
		record := row[strings.TrimSpace(join.LeftAlias)]
		if key, ok := reportJoinRecordKey(record, join.Equalities(), true); ok {
			if left[key] == nil {
				left[key] = map[string]bool{}
			}
			left[key][record.ID] = true
			if len(left[key]) > 1 {
				return reportJoinCardinalityError(join)
			}
		}
	}
	return nil
}

func reportValidateJoinedCardinality(rows []reportDatasetRow, join reportmodel.ReportDatasetJoin) error {
	rightRecords := []recordmodel.Record{}
	leftRows := make([]reportDatasetRow, 0, len(rows))
	seenRight := map[string]bool{}
	for _, row := range rows {
		leftRows = append(leftRows, row)
		if record := row[strings.TrimSpace(join.Alias)]; record != nil && !seenRight[record.ID] {
			seenRight[record.ID] = true
			rightRecords = append(rightRecords, *record)
		}
	}
	return reportValidateJoinCardinality(leftRows, rightRecords, join)
}

func reportJoinCardinalityError(join reportmodel.ReportDatasetJoin) error {
	return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.join_cardinality_violation", Params: map[string]string{"alias": strings.TrimSpace(join.Alias), "cardinality": strings.TrimSpace(join.Cardinality)}}
}
