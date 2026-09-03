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
	next    string
}

// projectExportPage is the sole Record-policy projection used by both direct
// delivery and the external Data Exchange owner.
func (s *RecordExportApplicationService) projectExportPage(ctx context.Context, prepared recordExportPrepared, cursor string) (recordExportProjectedPage, error) {
	if err := ctx.Err(); err != nil {
		return recordExportProjectedPage{}, err
	}
	queryOptions := prepared.options.Query
	queryOptions.Page, queryOptions.PageSize = 1, recordExportBatchSize
	queryOptions.SkipTotal, queryOptions.AfterID = true, strings.TrimSpace(cursor)
	// Export iteration has one canonical order. This makes the cursor stable and
	// prevents deep OFFSET pagination while preserving all filter/search scope.
	queryOptions.Sort = []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}
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
	next := ""
	if page.HasNext && len(page.Items) > 0 {
		next = page.Items[len(page.Items)-1].ID
	}
	return recordExportProjectedPage{columns: recordExportHeader(prepared.fields), rows: rows, hasNext: page.HasNext, next: next}, nil
}
