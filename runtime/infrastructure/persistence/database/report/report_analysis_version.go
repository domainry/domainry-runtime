package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/domainry/domainry-orm/query"
	model "github.com/domainry/domainry-report-sdk/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

// ReadReportAnalysisSourceVersion streams the full, scoped input projection
// within one read transaction. It retains only hashes, so a large dataset is
// never sampled or materialized in Report/Agent. Count+MAX(updated_at) cannot
// distinguish same-time content updates and is deliberately not used here.
func (s *ReportSQLStore) ReadReportAnalysisSourceVersion(ctx context.Context, request reportcontract.ReportSnapshotSourceVersionRequest) (model.ReportSnapshotSourceVersion, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return model.ReportSnapshotSourceVersion{}, fmt.Errorf("analysis source version store unavailable")
	}
	tx, err := s.beginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.ReportSnapshotSourceVersion{}, err
	}
	defer tx.Rollback()
	out := model.ReportSnapshotSourceVersion{SourceVersions: map[string]string{}}
	aliases := make([]string, 0, len(request.Objects))
	for alias := range request.Objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		object, scope := request.Objects[alias], request.Queries[alias]
		scope.SelectFields = append([]string{}, scope.SelectFields...)
		sort.Strings(scope.SelectFields)
		columns := reportStoreSourceColumns(scope.SelectFields)
		source := model.ReportObjectSQLSource{Alias: alias, ObjectKey: object.Key, Fields: columns}
		queries := map[string]recordmodel.RecordListQuery{alias: scope}
		builder, err := s.reportObjectSQLSourceSelect(ctx, tx, reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: request.WorkspaceID, Objects: request.Objects, Queries: queries}, source)
		if err != nil {
			return model.ReportSnapshotSourceVersion{}, err
		}
		statement, args, err := builder.OrderBy(query.Ascending("id")).Build()
		if err != nil {
			return model.ReportSnapshotSourceVersion{}, err
		}
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return model.ReportSnapshotSourceVersion{}, err
		}
		digest := sha256.New()
		encoder := json.NewEncoder(digest)
		if err := encoder.Encode(struct {
			Object, Alias string
			Columns       []string
		}{object.Key, alias, columns}); err != nil {
			rows.Close()
			return model.ReportSnapshotSourceVersion{}, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return model.ReportSnapshotSourceVersion{}, err
			}
			for i, value := range values {
				if bytes, ok := value.([]byte); ok {
					values[i] = string(bytes)
				}
				if columns[i] == "updated_at" && values[i] != nil {
					if stamp := fmt.Sprint(values[i]); stamp > out.Watermark {
						out.Watermark = stamp
					}
				}
			}
			if err := encoder.Encode(values); err != nil {
				rows.Close()
				return model.ReportSnapshotSourceVersion{}, err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return model.ReportSnapshotSourceVersion{}, err
		}
		if err := rows.Close(); err != nil {
			return model.ReportSnapshotSourceVersion{}, err
		}
		out.SourceVersions[alias] = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	}
	if err := tx.Commit(); err != nil {
		return model.ReportSnapshotSourceVersion{}, err
	}
	return out, nil
}

var _ reportcontract.ReportAnalysisSourceVersionReader = (*ReportSQLStore)(nil)
