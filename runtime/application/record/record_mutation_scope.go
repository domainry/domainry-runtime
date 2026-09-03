package record

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type RecordMutationScopeResolver func(principalmodel.Principal, definitionmodel.ObjectSchema, string) (*recordmodel.RecordScopeExpression, error)

// RecordMutationTargetLoader treats a client-supplied record ID only as a
// candidate. Production wiring loads the row through workspace + ID + the
// exact permission's compiled data-scope predicate.
type RecordMutationTargetLoader func(context.Context, string, definitionmodel.ObjectSchema, string, *recordmodel.RecordScopeExpression) (recordmodel.Record, bool, error)

func resolveRecordMutationScope(resolve RecordMutationScopeResolver, principal principalmodel.Principal, object definitionmodel.ObjectSchema, action string) (*recordmodel.RecordScopeExpression, error) {
	if resolve != nil {
		return resolve(principal, object, action)
	}
	if expression, err, handled := recordservice.RecordCompileSDKMutationScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, action); handled {
		if err != nil {
			return nil, apperror.New(apperror.KindForbidden, "backend.policy.expression_invalid", err, nil)
		}
		return expression, nil
	}
	if principal.SystemScope.Valid() && principal.Allows(object.Key, action) {
		return nil, nil
	}
	return nil, apperror.New(apperror.KindForbidden, "backend.permission.denied", nil, nil)
}

func recordRepositoryMutationTargetLoader(repository recordrepository.RecordRepository) RecordMutationTargetLoader {
	return func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, candidateID string, scope *recordmodel.RecordScopeExpression) (recordmodel.Record, bool, error) {
		query := recordmodel.RecordListQuery{
			Page:              1,
			PageSize:          1,
			SkipTotal:         true,
			Filters:           map[string]any{"id__in": []any{candidateID}},
			AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
			AfterID:           "",
			LockIntent:        recordmodel.RecordQueryLockNone,
		}
		if scope != nil {
			query.AuthorizationMode = recordmodel.RecordQueryAuthorizationPredicate
			query.RootObjectKey = object.Key
			query.ScopeExpression = scope
		}
		page, err := repository.ListRecords(ctx, workspaceID, object, query)
		if err != nil {
			return recordmodel.Record{}, false, err
		}
		if len(page.Items) == 0 {
			return recordmodel.Record{}, false, nil
		}
		return page.Items[0], true, nil
	}
}

func loadRecordMutationTarget(ctx context.Context, load RecordMutationTargetLoader, repository recordrepository.RecordRepository, workspaceID string, object definitionmodel.ObjectSchema, candidateID string, scope *recordmodel.RecordScopeExpression) (recordmodel.Record, bool, error) {
	if load != nil {
		return load(ctx, workspaceID, object, candidateID, scope)
	}
	// Directly constructed services in isolated unit tests retain the legacy
	// repository probe. Runtime production assembly always injects the scoped
	// loader above.
	return repository.GetRecord(ctx, workspaceID, object, candidateID)
}
