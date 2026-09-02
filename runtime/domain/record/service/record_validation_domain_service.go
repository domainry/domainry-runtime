package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordValidationDependencies struct {
	Repository         recordrepository.RecordRepository
	Object             func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	CanAccess          func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CanAccessPersisted func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error)
	Identity           identitysdk.Directory
}

// RecordValidationDomainService validates record mutations.
type RecordValidationDomainService struct {
	uniqueness      *RecordUniquenessValidator
	relations       *RecordRelationValidator
	relatedPolicies *RecordRelatedPolicyValidator
}

func NewRecordValidationDomainService(dependencies RecordValidationDependencies) *RecordValidationDomainService {
	return &RecordValidationDomainService{
		uniqueness: NewRecordUniquenessValidator(dependencies.Repository),
		relations: NewRecordRelationValidator(RecordRelationValidationDependencies{
			Repository: dependencies.Repository, Object: dependencies.Object,
			CanAccessRecord: dependencies.CanAccess, CanAccessPersistedRecord: dependencies.CanAccessPersisted, Identity: dependencies.Identity,
		}),
		relatedPolicies: NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{
			Repository: dependencies.Repository, Object: dependencies.Object,
			CanAccess: dependencies.CanAccess,
		}),
	}
}

func (s *RecordValidationDomainService) ValidateRelations(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
	return s.relations.Validate(ctx, object, data, principal)
}

func (s *RecordValidationDomainService) ValidateDomainPolicies(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
	return s.relatedPolicies.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal, nil)
}

func (s *RecordValidationDomainService) ValidateDuplicateIdentity(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	return s.uniqueness.ValidateDuplicateIdentity(ctx, workspaceID, object, currentID, data)
}

func (s *RecordValidationDomainService) ValidateUnique(ctx context.Context, workspaceID, objectKey string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	return s.uniqueness.ValidateUnique(ctx, workspaceID, objectKey, object, currentID, data)
}

func (s *RecordValidationDomainService) ValidateStateMachinePolicies(object definitionmodel.ObjectSchema, before, next map[string]any, principal principalmodel.Principal) error {
	return recordvalidation.RecordValidateStateMachinePolicies(object, before, next, principal)
}
