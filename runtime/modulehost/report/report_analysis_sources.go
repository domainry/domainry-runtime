package reportmodulehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	reportsdk "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-report-sdk/modulehost"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

// ReportAnalysisSources projects current Runtime metadata and authorization;
// analysis specifications and calculations remain inside the Report owner.
func (h *ReportModuleQueryHost) ReportAnalysisSources(ctx context.Context, subject model.ReportSubject) ([]model.AnalysisDataset, error) {
	if h == nil || h.dependencies.Access == nil || h.dependencies.AnalysisObjectKeys == nil {
		return nil, stableReportHostError(nil)
	}
	authorizer, ok := h.dependencies.Access.(reportcontract.ReportObjectSQLFieldAuthorizer)
	if !ok {
		return nil, stableReportHostError(nil)
	}
	principal := RuntimePrincipalFromReportSubject(subject)
	keys := append([]string{}, h.dependencies.AnalysisObjectKeys()...)
	sort.Strings(keys)
	result := []model.AnalysisDataset{}
	seen := map[string]bool{}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[key] || !subject.HasPermission(key+".read") {
			continue
		}
		seen[key] = true
		object, err := h.dependencies.Access.ReportObjectForAction(ctx, principal, key, "read")
		if err != nil {
			if analysisDiscoveryDenied(err) {
				continue
			}
			return nil, stableReportHostError(err)
		}
		projected, err := reportcontract.ReportEngineObjects(map[string]definitionmodel.ObjectSchema{key: object})
		if err != nil {
			return nil, stableReportHostError(err)
		}
		dataset := model.AnalysisDataset{Key: key, Name: object.Name, Kind: "business_object", Columns: []model.AnalysisColumn{}}
		add := func(column model.AnalysisColumn) error {
			if err := authorizer.AuthorizeReportObjectSQLField(ctx, principal, object, column.Key); err != nil {
				if analysisDiscoveryDenied(err) {
					return nil
				}
				return stableReportHostError(err)
			}
			dataset.Columns = append(dataset.Columns, column)
			return nil
		}
		for _, field := range projected[key].Fields {
			kind := strings.TrimSpace(field.Type)
			switch kind {
			case "text", "email", "long_text", "phone", "relation", "select", "url", "user":
				kind = "text"
			case "number", "boolean", "date", "datetime", "decimal", "integer", "currency", "percent":
			default:
				continue
			}
			column := model.AnalysisColumn{Key: field.Key, Type: kind, Precision: field.Precision}
			if field.Scale > 0 {
				column.Scale = int(field.Scale)
			}
			if kind == "percent" {
				column.Unit = "ratio"
			}
			if kind == "currency" {
				for _, authored := range object.Fields {
					if authored.Key == field.Key {
						config, err := recordmodel.RecordNormalizeDecimalConfig(authored.Config)
						if err != nil {
							return nil, stableReportHostError(err)
						}
						if config.CurrencyCode != "XXX" {
							column.Unit = config.CurrencyCode
						}
					}
				}
			}
			if err := add(column); err != nil {
				return nil, err
			}
		}
		// Envelope fields are supported by the existing Report SQL compiler and
		// follow the same object/field policy checks as every other source field.
		for _, column := range []model.AnalysisColumn{{Key: "id", Type: "text"}, {Key: "created_at", Type: "datetime"}, {Key: "updated_at", Type: "datetime"}} {
			duplicate := false
			for _, existing := range dataset.Columns {
				if existing.Key == column.Key {
					duplicate = true
					break
				}
			}
			if !duplicate {
				if err := add(column); err != nil {
					return nil, err
				}
			}
		}
		if len(dataset.Columns) == 0 {
			continue
		}
		sort.Slice(dataset.Columns, func(i, j int) bool { return dataset.Columns[i].Key < dataset.Columns[j].Key })
		content, err := json.Marshal(object)
		if err != nil {
			return nil, stableReportHostError(err)
		}
		digest := sha256.Sum256(content)
		dataset.Version = hex.EncodeToString(digest[:])
		dataset.References = []model.AnalysisReference{{Kind: "business_object", ID: key, Label: object.Name, Version: dataset.Version}}
		result = append(result, dataset)
	}
	return result, nil
}

func analysisDiscoveryDenied(err error) bool {
	var failure *reportsdk.Error
	return errors.As(stableReportHostError(err), &failure) && (failure.StatusCode == 403 || failure.StatusCode == 404)
}

var _ modulehost.AnalysisSources = (*ReportModuleQueryHost)(nil)

func (h *ReportModuleQueryHost) ReadReportAnalysisSourceVersion(ctx context.Context, report model.ReportSchema, subject model.ReportSubject) (model.ReportSnapshotSourceVersion, error) {
	if h == nil || h.dependencies.Access == nil {
		return model.ReportSnapshotSourceVersion{}, stableReportHostError(nil)
	}
	reader, ok := h.dependencies.SnapshotSources.(reportcontract.ReportAnalysisSourceVersionReader)
	if !ok {
		return model.ReportSnapshotSourceVersion{}, stableReportHostError(nil)
	}
	request, err := h.reportSourceVersionRequest(ctx, report, RuntimePrincipalFromReportSubject(subject))
	if err != nil {
		return model.ReportSnapshotSourceVersion{}, stableReportHostError(err)
	}
	result, err := reader.ReadReportAnalysisSourceVersion(ctx, request)
	if err != nil {
		return model.ReportSnapshotSourceVersion{}, stableReportHostError(err)
	}
	return result, nil
}

var _ modulehost.AnalysisSourceVersionReader = (*ReportModuleQueryHost)(nil)
