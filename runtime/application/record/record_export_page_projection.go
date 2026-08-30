package record

import (
	"context"
	"fmt"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type recordExportProjectedPage struct {
	columns []string
	rows    [][]string
	hasNext bool
}

// projectExportPage is the sole Record-policy projection used by both the
// synchronous compatibility response and the external Data Exchange owner.
func (s *RecordExportApplicationService) projectExportPage(ctx context.Context, prepared recordExportPrepared, pageNumber int) (recordExportProjectedPage, error) {
	if err := ctx.Err(); err != nil {
		return recordExportProjectedPage{}, err
	}
	queryOptions := prepared.options.Query
	queryOptions.Page, queryOptions.PageSize = pageNumber, recordExportBatchSize
	query := queryOptions
	if s.dependencies.NormalizeQuery != nil {
		query = s.dependencies.NormalizeQuery(prepared.object, queryOptions, prepared.principal)
	}
	page, err := s.dependencies.Repository.ListRecords(ctx, prepared.principal.WorkspaceID, prepared.object, query)
	if err != nil {
		return recordExportProjectedPage{}, recordExportInternalError("export records", err)
	}
	relationLabels := s.relationLabels(ctx, prepared.object, prepared.fields, page.Items, prepared.principal)
	projectedByID := map[string]recordmodel.Record{}
	if s.dependencies.ProjectRecords != nil {
		projectedRecords, projectErr := s.dependencies.ProjectRecords(ctx, prepared.principal, prepared.object, page.Items, "export")
		if projectErr != nil {
			return recordExportProjectedPage{}, projectErr
		}
		for _, projected := range projectedRecords {
			projectedByID[projected.ID] = projected
		}
	}
	rows := make([][]string, 0, len(page.Items))
	for _, record := range page.Items {
		if err := ctx.Err(); err != nil {
			return recordExportProjectedPage{}, err
		}
		if query.ScopeExpression == nil && s.dependencies.CanAccess != nil && !s.dependencies.CanAccess(prepared.principal, prepared.object, record) {
			continue
		}
		projected := record
		if s.dependencies.ProjectRecords != nil {
			projected = projectedByID[record.ID]
		}
		row := []string{record.ID, record.CreatedAt, record.UpdatedAt}
		for _, field := range prepared.fields {
			value := ""
			if s.dependencies.ProjectRecords != nil {
				if projectedValue, present := projected.Data[field.Key]; present && projectedValue != nil {
					value = fmt.Sprint(projectedValue)
				}
			} else {
				value, err = recordExportFieldValue(prepared.principal, prepared.object.Key, field, record.Data[field.Key])
				if err != nil {
					return recordExportProjectedPage{}, err
				}
			}
			row = append(row, value)
			if field.Type == "relation" {
				display := ""
				if _, visible := projected.Data[field.Key]; visible {
					display = relationLabels[field.Key][strings.TrimSpace(fmt.Sprint(record.Data[field.Key]))]
				}
				row = append(row, display)
			}
		}
		rows = append(rows, row)
	}
	return recordExportProjectedPage{columns: recordExportHeader(prepared.fields), rows: rows, hasNext: page.HasNext}, nil
}
