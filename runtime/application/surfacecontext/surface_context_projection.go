package surfacecontext

import (
	"fmt"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"
)

func SurfaceContextRecordDisplay(object definitionmodel.ObjectSchema, record recordmodel.Record) (string, string) {
	if display, ok := object.UX["display"].(map[string]any); ok {
		if key := strings.TrimSpace(fmt.Sprint(display["title_field"])); key != "" && key != "<nil>" {
			if value := strings.TrimSpace(fmt.Sprint(record.Data[key])); value != "" && value != "<nil>" {
				return key, value
			}
		}
	}
	for _, key := range []string{"name", "title", "subject", "number", "code", "display_name", "short_name"} {
		if value := strings.TrimSpace(fmt.Sprint(record.Data[key])); value != "" && value != "<nil>" {
			return key, value
		}
	}
	return "id", record.ID
}

func SurfaceContextReferencePermission(principal principalmodel.Principal, sourceObjectKey, relationFieldKey, targetObjectKey string) (identitysdk.ReferencePolicy, bool) {
	if principal.AccessBundle != nil {
		for _, permission := range identityevaluator.AllowedReferences(*principal.AccessBundle, identitysdk.ResourceType(sourceObjectKey)) {
			if strings.TrimSpace(permission.Reference) != relationFieldKey || strings.TrimSpace(string(permission.TargetResource)) != targetObjectKey {
				continue
			}
			return permission, true
		}
		return identitysdk.ReferencePolicy{}, false
	}
	return identitysdk.ReferencePolicy{}, false
}

func SurfaceContextAppendReferenceDisplayFields(existing, fields []string) []string {
	seen := map[string]bool{}
	for _, field := range existing {
		if field = strings.TrimSpace(field); field != "" {
			seen[field] = true
		}
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" && !seen[field] {
			seen[field] = true
			existing = append(existing, field)
		}
	}
	return existing
}

func SurfaceContextPermittedRecordDisplay(object definitionmodel.ObjectSchema, record recordmodel.Record, displayFields []string) (string, string) {
	for _, field := range displayFields {
		field = strings.TrimSpace(field)
		if value := strings.TrimSpace(fmt.Sprint(record.Data[field])); field != "" && value != "" && value != "<nil>" {
			return field, value
		}
	}
	if len(displayFields) == 0 {
		return SurfaceContextRecordDisplay(object, record)
	}
	if value := strings.TrimSpace(fmt.Sprint(record.Data[displayFields[0]])); value != "" && value != "<nil>" {
		return displayFields[0], value
	}
	return "id", record.ID
}

func SurfaceContextMetrics(objects map[string]surfacecontextmodel.SurfaceContextObjectResult) map[string]any {
	metrics := map[string]any{"objects": map[string]any{}}
	objectMetrics := metrics["objects"].(map[string]any)
	for objectKey, objectResult := range objects {
		objectMetrics[objectKey] = map[string]any{"count": len(objectResult.Page.Items), "total": objectResult.Page.Total, "has_next": objectResult.Page.HasNext}
	}
	return metrics
}

func SurfaceContextMaskedFields(principal principalmodel.Principal, object definitionmodel.ObjectSchema) []string {
	masked := []string{}
	if principal.AccessBundle != nil {
		for _, policy := range principal.AccessBundle.FieldPolicies {
			if policy.Resource == identitysdk.ResourceType(object.Key) && policy.Read && policy.Masked {
				masked = append(masked, policy.Field)
			}
		}
		sort.Strings(masked)
		return masked
	}
	sort.Strings(masked)
	return masked
}

func SurfaceContextRelationLabelCount(labels map[string]map[string]string) int {
	count := 0
	for _, byID := range labels {
		count += len(byID)
	}
	return count
}

func SurfaceContextReportSourceObjects(report reportmodel.ReportSchema) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, objectKey := range reportmodel.ReportDatasetObjectKeys(report.Dataset) {
		objectKey = strings.TrimSpace(objectKey)
		if objectKey != "" && !seen[objectKey] {
			seen[objectKey] = true
			out = append(out, objectKey)
		}
	}
	return out
}

func SurfaceContextSortedIDs(ids map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func SurfaceContextValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
