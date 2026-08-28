package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"

	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordScopeOwnerFactDerivationDependencies struct {
	WorkforceDirectory identitysdk.Directory
}

// RecordScopeOwnerFactDerivationDomainService projects Identity-owned
// workforce facts onto Runtime-owned business records. It never persists or
// derives Identity directory objects.
type RecordScopeOwnerFactDerivationDomainService struct {
	workforceDirectory identitysdk.Directory
}

func NewRecordScopeOwnerFactDerivationDomainService(dependencies RecordScopeOwnerFactDerivationDependencies) *RecordScopeOwnerFactDerivationDomainService {
	return &RecordScopeOwnerFactDerivationDomainService{workforceDirectory: dependencies.WorkforceDirectory}
}

func (s *RecordScopeOwnerFactDerivationDomainService) Apply(ctx context.Context, _ string, object definitionmodel.ObjectSchema, data map[string]any, _ string) error {
	if s == nil {
		return recordInternalError("derive scope owner facts", errors.New("scope owner fact deriver is unavailable"))
	}
	return s.applyScopeOwnerDepartment(ctx, object, data)
}

func (s *RecordScopeOwnerFactDerivationDomainService) applyScopeOwnerDepartment(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any) error {
	ownerField := recordpolicy.RecordScopeOwnerFieldKey(object)
	if ownerField == "" || data == nil {
		return nil
	}
	departmentIDField := recordpolicy.RecordOwnerDepartmentIDFieldKey(object)
	departmentPathField := recordpolicy.RecordOwnerDepartmentPathFieldKey(object)
	if departmentIDField == "" || departmentPathField == "" {
		return recordServiceError(apperror.KindBadRequest, "backend.record.scope_owner_department_contract_invalid", nil, "object", object.Key)
	}
	ownerID := strings.TrimSpace(fmt.Sprint(data[ownerField]))
	if ownerID == "" || ownerID == "<nil>" {
		data[departmentIDField], data[departmentPathField] = nil, nil
		return nil
	}
	if s.workforceDirectory == nil {
		return recordInternalError("resolve scope owner department", errors.New("workforce directory is unavailable"))
	}
	entries, err := s.workforceDirectory.ListWorkforce(ctx, identitysdk.DirectoryQuery{})
	if err != nil {
		return recordInternalError("resolve scope owner department", err)
	}
	var matched *identitysdk.WorkforceEntry
	for index := range entries {
		entry := &entries[index]
		if strings.TrimSpace(entry.IdentityUserID) != ownerID {
			continue
		}
		if strings.TrimSpace(entry.OrganizationUnitID) == "" || strings.TrimSpace(entry.OrganizationPath) == "" {
			continue
		}
		if matched != nil {
			return recordServiceError(apperror.KindConflict, "backend.record.scope_owner_department_ambiguous", nil, "owner_id", ownerID)
		}
		matched = entry
	}
	if matched == nil {
		return recordServiceError(apperror.KindBadRequest, "backend.record.scope_owner_department_unresolved", nil, "owner_id", ownerID)
	}
	data[departmentIDField] = strings.TrimSpace(matched.OrganizationUnitID)
	data[departmentPathField] = strings.TrimSpace(matched.OrganizationPath)
	return nil
}
