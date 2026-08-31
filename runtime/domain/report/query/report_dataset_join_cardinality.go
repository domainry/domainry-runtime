package query

import (
	"errors"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report/query/engine"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func reportValidateJoinCardinality(leftRows []reportDatasetRow, rightRecords []recordmodel.Record, join reportmodel.ReportDatasetJoin) error {
	return reportJoinError(reportengine.ValidateJoinCardinality(reportEngineRows(leftRows), reportEngineRecords(rightRecords), join))
}

func reportValidateJoinedCardinality(rows []reportDatasetRow, join reportmodel.ReportDatasetJoin) error {
	return reportJoinError(reportengine.ValidateJoinedCardinality(reportEngineRows(rows), join))
}

func reportJoinError(err error) error {
	if err == nil {
		return nil
	}
	var violation *reportengine.JoinCardinalityError
	if errors.As(err, &violation) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.join_cardinality_violation", Params: map[string]string{"alias": violation.Alias, "cardinality": violation.Cardinality}}
	}
	return err
}
