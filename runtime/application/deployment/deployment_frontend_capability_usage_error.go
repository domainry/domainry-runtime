package deployment

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

import (
	"sort"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func firstFrontendCapabilityUsageError(issues []deploymentmodel.FrontendCapabilityUsageValidationIssue) error {
	for _, issue := range issues {
		if issue.Severity != "error" {
			continue
		}
		params := make(map[string]string, len(issue.Params)+1)
		for key, value := range issue.Params {
			params[key] = value
		}
		params["field"] = issue.FieldPath
		keys := make([]string, 0, len(params))
		for key := range params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := make([]string, 0, len(keys)*2)
		for _, key := range keys {
			values = append(values, key, params[key])
		}
		return applicationError(apperror.KindBadRequest, issue.ErrorCode, nil, values...)
	}
	return nil
}
