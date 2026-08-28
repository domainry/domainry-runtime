package service

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// RecordQueryPolicy is the authorization and schema boundary required by Record reads.
// Implementations remain free to source schema and permission state elsewhere.
type RecordQueryPolicy interface {
	ObjectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	EnsureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error
	NormalizeListQuery(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery
	CanAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
}

type RecordReadDependencies struct {
	Repository                recordrepository.RecordRepository
	Policy                    RecordQueryPolicy
	IdentityProfileExtensions func() []profilebindingmodel.Binding
	ContextualFieldPolicy     *RecordContextualFieldPolicyDomainService
	AuditFieldDenials         func(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, string, []RecordFieldPolicyDecision, principalmodel.Principal)
	AuditScopeDenial          func(context.Context, definitionmodel.ObjectSchema, string, principalmodel.Principal)
}

// RecordReadDomainService reads records.
type RecordReadDomainService struct {
	repository                recordrepository.RecordRepository
	policy                    RecordQueryPolicy
	identityProfileExtensions func() []profilebindingmodel.Binding
	contextualFieldPolicy     *RecordContextualFieldPolicyDomainService
	auditFieldDenials         func(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, string, []RecordFieldPolicyDecision, principalmodel.Principal)
	auditScopeDenial          func(context.Context, definitionmodel.ObjectSchema, string, principalmodel.Principal)
}

type RecordIdentityProfileReference struct {
	ObjectKey             string `json:"object_key"`
	IdentityRelationField string `json:"identity_relation_field"`
	RecordCount           int    `json:"record_count"`
	DeletePolicy          string `json:"delete_policy"`
}

func NewRecordReadDomainService(dependencies RecordReadDependencies) *RecordReadDomainService {
	return &RecordReadDomainService{
		repository:                dependencies.Repository,
		policy:                    dependencies.Policy,
		identityProfileExtensions: dependencies.IdentityProfileExtensions,
		contextualFieldPolicy:     dependencies.ContextualFieldPolicy,
		auditFieldDenials:         dependencies.AuditFieldDenials,
		auditScopeDenial:          dependencies.AuditScopeDenial,
	}
}

func (s *RecordReadDomainService) IdentityProfileReferences(ctx context.Context, userID string, principal principalmodel.Principal) ([]RecordIdentityProfileReference, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || s.identityProfileExtensions == nil {
		return []RecordIdentityProfileReference{}, nil
	}
	references := []RecordIdentityProfileReference{}
	for _, extension := range s.identityProfileExtensions() {
		page, err := s.ListRecords(ctx, extension.ObjectKey, recordmodel.RecordListQuery{
			Page:     1,
			PageSize: 1,
			Filters:  map[string]any{extension.IdentityRelationField: userID},
		}, principal)
		if err != nil {
			return nil, err
		}
		if page.Total > 0 {
			references = append(references, RecordIdentityProfileReference{
				ObjectKey:             extension.ObjectKey,
				IdentityRelationField: extension.IdentityRelationField,
				RecordCount:           page.Total,
				DeletePolicy:          "retain_and_block_identity_hard_delete",
			})
		}
	}
	return references, nil
}

func (s *RecordReadDomainService) ListRecords(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.listRecords(ctx, objectKey, query, principal, true)
}

// ListRecordsForAction preserves object authorization and record scope while
// returning the canonical business fields required by a governed Action
// Handler. Presentation masking belongs to external reads, not to the
// source-owned business-rule execution boundary.
func (s *RecordReadDomainService) ListRecordsForAction(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.listRecords(ctx, objectKey, query, principal, false)
}

func (s *RecordReadDomainService) listRecords(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal, applyFieldPolicy bool) (recordmodel.RecordPageResult, error) {
	object, err := s.policy.ObjectForAction(principal, objectKey, "read")
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	if err := s.policy.EnsureReportSnapshotAccess(object, "read", principal); err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	query = s.policy.NormalizeListQuery(object, query, principal)
	page, err := s.repository.ListRecords(ctx, principal.WorkspaceID, object, query)
	if err != nil {
		return recordmodel.RecordPageResult{}, recordInternalError("list records", err)
	}
	visible := make([]recordmodel.Record, 0, len(page.Items))
	for _, record := range page.Items {
		if query.ScopeExpression != nil || s.policy.CanAccessRecord(principal, object, record) {
			visible = append(visible, record)
		}
	}
	if len(visible) != len(page.Items) {
		// A repository page can still require the application fallback policy
		// above (for example, a scope that cannot be compiled into SQL). Never
		// expose the pre-authorization count or pagination bit beside an empty
		// scoped result: both disclose records the principal cannot read.
		page.Total = len(visible)
		page.HasNext = false
	}
	if !applyFieldPolicy {
		page.Items = visible
		return page, nil
	}
	if s.contextualFieldPolicy == nil {
		page.Items = make([]recordmodel.Record, 0, len(visible))
		for _, record := range visible {
			page.Items = append(page.Items, recordpolicy.RecordFilterReadable(principal, object, record))
		}
		return page, nil
	}
	filtered, denials, err := s.contextualFieldPolicy.ApplyReadPage(ctx, principal, object, visible, "read")
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	if s.auditFieldDenials != nil {
		for _, record := range visible {
			if recordDenials := denials[record.ID]; len(recordDenials) > 0 {
				s.auditFieldDenials(ctx, object, record, "read", recordDenials, principal)
			}
		}
	}
	page.Items = filtered
	return page, nil
}

func (s *RecordReadDomainService) GetRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.getRecord(ctx, objectKey, recordID, principal, recordmodel.RecordQueryLockNone, true)
}

func (s *RecordReadDomainService) GetRecordForUpdate(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.getRecord(ctx, objectKey, recordID, principal, recordmodel.RecordQueryLockForUpdate, true)
}

func (s *RecordReadDomainService) GetRecordForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.getRecord(ctx, objectKey, recordID, principal, recordmodel.RecordQueryLockNone, false)
}

func (s *RecordReadDomainService) GetRecordForUpdateForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.getRecord(ctx, objectKey, recordID, principal, recordmodel.RecordQueryLockForUpdate, false)
}

func (s *RecordReadDomainService) getRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal, lockIntent string, applyFieldPolicy bool) (recordmodel.Record, error) {
	object, err := s.policy.ObjectForAction(principal, objectKey, "read")
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.policy.EnsureReportSnapshotAccess(object, "read", principal); err != nil {
		return recordmodel.Record{}, err
	}
	query := s.policy.NormalizeListQuery(object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, Filters: map[string]any{"id__in": []any{recordID}}, LockIntent: lockIntent}, principal)
	page, err := s.repository.ListRecords(ctx, principal.WorkspaceID, object, query)
	if err != nil {
		return recordmodel.Record{}, recordInternalError("get record", err)
	}
	if len(page.Items) == 0 {
		// A locking read runs inside the Action-owned transaction. Persisting a
		// denial audit through the ordinary repository connection here can
		// deadlock SQLite's BEGIN IMMEDIATE owner. The Action failure boundary
		// uses the attached object/record identity to commit the denial audit
		// together with the failed execution receipt after rollback.
		if lockIntent == recordmodel.RecordQueryLockNone && s.auditScopeDenial != nil && recordScopeAuditsDenial(principal, object.Key) {
			s.auditScopeDenial(ctx, object, recordID, principal)
		}
		return recordmodel.Record{}, recordServiceError(
			apperror.KindNotFound,
			"backend.record.not_found",
			nil,
			"object_key",
			object.Key,
			"record_id",
			recordID,
		)
	}
	if !applyFieldPolicy {
		return page.Items[0], nil
	}
	return s.applyFieldPolicy(ctx, principal, object, page.Items[0], "read")
}

func recordScopeAuditsDenial(principal principalmodel.Principal, objectKey string) bool {
	objectKey = strings.TrimSpace(objectKey)
	if principal.AccessBundle == nil {
		return false
	}
	for _, policy := range principal.AccessBundle.DataPolicies {
		if policy.Action == "read" && policy.AuditDenial && strings.TrimSpace(string(policy.Resource)) == objectKey {
			return true
		}
	}
	return false
}

func (s *RecordReadDomainService) applyFieldPolicy(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, action string) (recordmodel.Record, error) {
	if s.contextualFieldPolicy == nil {
		return recordpolicy.RecordFilterReadable(principal, object, record), nil
	}
	filtered, denials, err := s.contextualFieldPolicy.ApplyRead(ctx, principal, object, record, action)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if len(denials) > 0 && s.auditFieldDenials != nil {
		s.auditFieldDenials(ctx, object, record, action, denials, principal)
	}
	return filtered, nil
}
