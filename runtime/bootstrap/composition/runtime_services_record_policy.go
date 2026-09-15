package composition

import (
	"context"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func newRecordQueryPolicyService(services *runtimeAssembly) *recordservice.RecordQueryPolicyDomainService {
	return recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return recordSchemaObjects(services)
		},
		ReportObjects: func() map[string]struct{} {
			services.mu.RLock()
			defer services.mu.RUnlock()
			result := make(map[string]struct{}, len(services.reportObjects))
			for key := range services.reportObjects {
				result[key] = struct{}{}
			}
			return result
		},
		CandidateScopeMatches: func(ctx context.Context, workspaceID string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression) (bool, error) {
			evaluator, ok := services.recordRepo.(interface {
				CandidateScopeMatches(context.Context, string, recordmodel.Record, recordmodel.RecordScopeExpression) (bool, error)
			})
			if !ok {
				return false, fmt.Errorf("record candidate scope evaluator is unavailable")
			}
			return evaluator.CandidateScopeMatches(ctx, workspaceID, candidate, expression)
		},
	})
}
