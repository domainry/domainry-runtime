package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	partymodel "github.com/domainry/domainry-party-sdk/contract"
	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RecordPartyReferenceLookup interface {
	Get(context.Context, string) (partymodel.Aggregate, bool, error)
}

type RecordRelationValidationDependencies struct {
	Repository               recordrepository.RecordRepository
	Object                   func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	CanAccessRecord          func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CanAccessPersistedRecord func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error)
	Identity                 identitysdk.Directory
	Party                    RecordPartyReferenceLookup
}

type RecordRelationValidator struct {
	repository               recordrepository.RecordRepository
	object                   func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	canAccessRecord          func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	canAccessPersistedRecord func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error)
	identity                 identitysdk.Directory
	party                    RecordPartyReferenceLookup
}

type recordPlannedRelationsContextKey struct{}

// RecordWithPlannedRelations exposes records already planned in the current
// canonical mutation batch. It permits ordered intra-batch references without
// weakening normal repository-backed relation validation.
func RecordWithPlannedRelations(ctx context.Context, records map[string]map[string]recordmodel.Record) context.Context {
	if ctx == nil || len(records) == 0 {
		return ctx
	}
	cloned := make(map[string]map[string]recordmodel.Record, len(records))
	for objectKey, values := range records {
		cloned[objectKey] = make(map[string]recordmodel.Record, len(values))
		for recordID, record := range values {
			copyRecord := record
			copyRecord.Data = recordvalidation.RecordCloneData(record.Data)
			cloned[objectKey][recordID] = copyRecord
		}
	}
	return context.WithValue(ctx, recordPlannedRelationsContextKey{}, cloned)
}

func recordPlannedRelation(ctx context.Context, objectKey, recordID string) (recordmodel.Record, bool) {
	if ctx == nil {
		return recordmodel.Record{}, false
	}
	records, _ := ctx.Value(recordPlannedRelationsContextKey{}).(map[string]map[string]recordmodel.Record)
	record, found := records[strings.TrimSpace(objectKey)][strings.TrimSpace(recordID)]
	return record, found
}

// RecordPlannedRelations returns a defensive copy of records already staged in
// the current Action unit of work. Persistence authorization uses this to
// evaluate relation-path create scopes before those related records are
// committed and therefore visible to SQL.
func RecordPlannedRelations(ctx context.Context) map[string]map[string]recordmodel.Record {
	if ctx == nil {
		return nil
	}
	records, _ := ctx.Value(recordPlannedRelationsContextKey{}).(map[string]map[string]recordmodel.Record)
	if len(records) == 0 {
		return nil
	}
	result := make(map[string]map[string]recordmodel.Record, len(records))
	for objectKey, byID := range records {
		result[objectKey] = make(map[string]recordmodel.Record, len(byID))
		for recordID, record := range byID {
			result[objectKey][recordID] = recordmodel.Record{
				ID:        record.ID,
				Data:      recordvalidation.RecordCloneData(record.Data),
				CreatedAt: record.CreatedAt,
				UpdatedAt: record.UpdatedAt,
			}
		}
	}
	return result
}

func NewRecordRelationValidator(dependencies RecordRelationValidationDependencies) *RecordRelationValidator {
	return &RecordRelationValidator{
		repository:               dependencies.Repository,
		object:                   dependencies.Object,
		canAccessRecord:          dependencies.CanAccessRecord,
		canAccessPersistedRecord: dependencies.CanAccessPersistedRecord,
		identity:                 dependencies.Identity,
		party:                    dependencies.Party,
	}
}

func (s *RecordRelationValidator) Validate(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
	for _, field := range object.Fields {
		if field.Type != "relation" || recordvalidation.RecordIsEmptyValue(data[field.Key]) {
			continue
		}
		target := recordvalidation.RecordRelationTarget(field)
		if target == "" {
			continue
		}
		recordID := strings.TrimSpace(fmt.Sprint(data[field.Key]))
		if target == definitioncontract.IdentityUserObjectKey {
			if s.identity == nil {
				return recordInternalError("check identity user relation", fmt.Errorf("identity directory is not configured"))
			}
			_, found, err := s.identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(recordID)})
			if err != nil {
				return recordInternalError("check identity user relation", err)
			}
			if !found {
				return recordServiceError(apperror.KindBadRequest, "backend.relation.record_missing", nil, "field", field.Key, "object", target)
			}
			continue
		}
		if target == definitioncontract.IdentityDepartmentObjectKey || target == definitioncontract.IdentityOrganizationUnitObjectKey {
			if s.identity == nil {
				return recordInternalError("check identity department relation", fmt.Errorf("identity directory is not configured"))
			}
			_, found, err := s.identity.FindDepartment(ctx, identitysdk.DepartmentLookup{DepartmentID: recordID})
			if err != nil {
				return recordInternalError("check identity department relation", err)
			}
			if !found {
				return recordServiceError(apperror.KindBadRequest, "backend.relation.record_missing", nil, "field", field.Key, "object", target)
			}
			continue
		}
		if target == definitioncontract.IdentityWorkforceProfileObjectKey {
			entries, err := s.identity.ListWorkforce(ctx, identitysdk.DirectoryQuery{})
			if err != nil {
				return recordInternalError("check identity workforce relation", err)
			}
			found := false
			for _, entry := range entries {
				if entry.WorkforceProfileID == recordID {
					found = true
					break
				}
			}
			if !found {
				return recordServiceError(apperror.KindBadRequest, "backend.relation.record_missing", nil, "field", field.Key, "object", target)
			}
			continue
		}
		if target == partymodel.PartyObjectKey || target == partymodel.PersonObjectKey || target == partymodel.OrganizationObjectKey {
			if s.party == nil {
				return recordInternalError("check party relation", fmt.Errorf("party directory is not configured"))
			}
			party, found, err := s.party.Get(ctx, recordID)
			if err != nil {
				return recordInternalError("check party relation", err)
			}
			expectedKind := ""
			if target == partymodel.PersonObjectKey {
				expectedKind = partymodel.PartyKindPerson
			}
			if target == partymodel.OrganizationObjectKey {
				expectedKind = partymodel.PartyKindOrganization
			}
			if !found || expectedKind != "" && party.Party.Kind != expectedKind {
				return recordServiceError(apperror.KindBadRequest, "backend.relation.record_missing", nil, "field", field.Key, "object", target)
			}
			continue
		}
		targetObject, ok := s.object(ctx, target)
		if !ok {
			return recordServiceError(apperror.KindBadRequest, "backend.relation.target_unavailable", nil, "field", field.Key, "object", target)
		}
		if record, found := recordPlannedRelation(ctx, target, recordID); found {
			allowed, err := s.canAccess(ctx, principal, targetObject, record)
			if err != nil {
				return err
			}
			if !allowed {
				return recordServiceError(apperror.KindForbidden, "backend.record.outside_scope", nil)
			}
			continue
		}
		record, found, err := s.repository.GetRecord(ctx, principal.WorkspaceID, targetObject, recordID)
		if err != nil {
			return recordInternalError("check relation field", err)
		}
		if !found {
			return recordServiceError(apperror.KindBadRequest, "backend.relation.record_missing", nil, "field", field.Key, "object", target)
		}
		allowed, err := s.canAccess(ctx, principal, targetObject, record)
		if err != nil {
			return err
		}
		if !allowed {
			return recordServiceError(apperror.KindForbidden, "backend.record.outside_scope", nil)
		}
	}
	return nil
}

func (s *RecordRelationValidator) canAccess(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) (bool, error) {
	if s.canAccessPersistedRecord != nil {
		return s.canAccessPersistedRecord(ctx, principal, object, record)
	}
	if s.canAccessRecord == nil {
		return true, nil
	}
	return s.canAccessRecord(principal, object, record), nil
}
