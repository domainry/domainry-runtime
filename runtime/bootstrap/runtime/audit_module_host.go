package runtime

import (
	"context"
	"strings"

	"github.com/domainry/domainry-audit-sdk/contract"
	auditmodulehost "github.com/domainry/domainry-audit-sdk/modulehost"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type runtimeAuditApplicationHost struct {
	recordAccess *auditapplication.AuditRecordAccessApplicationService
	principals   *principalapplication.BusinessPrincipalApplicationService
	exportKey    []byte
}

func newRuntimeAuditApplicationHost(records *recordapplication.RecordApplicationService, repository recordrepository.RecordRepository, schema func() appschemamodel.ApplicationSchemaSnapshot, exportKey []byte) runtimeAuditApplicationHost {
	principalResolver := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
		Records: repository,
		Objects: func() []definitionmodel.ObjectSchema {
			return append([]definitionmodel.ObjectSchema(nil), schema().Objects...)
		},
		Extensions: func() []profilebindingmodel.Binding {
			return append([]profilebindingmodel.Binding(nil), schema().IdentityProfileExtensions...)
		},
	})
	recordAccess := auditapplication.NewAuditRecordAccessApplicationService(records, func() []definitionmodel.ObjectSchema {
		return append([]definitionmodel.ObjectSchema(nil), schema().Objects...)
	})
	return runtimeAuditApplicationHost{recordAccess: recordAccess, principals: principalResolver, exportKey: append([]byte(nil), exportKey...)}
}

func (h runtimeAuditApplicationHost) ResolveAuditSurfacePrincipal(ctx context.Context, request auditmodulehost.AuditSurfacePrincipalRequest) (auditmodulehost.AuditSurfacePrincipal, error) {
	principal := principalmodel.NewPrincipalFromIdentity(request.Identity, strings.TrimSpace(request.RequestID))
	principal.CorrelationID = strings.TrimSpace(request.CorrelationID)
	resolved, err := h.principals.ResolveBusinessPrincipal(ctx, principal, request.BusinessProfileKey, request.BusinessProfileID)
	if err != nil {
		return auditmodulehost.AuditSurfacePrincipal{}, err
	}
	return auditmodulehost.AuditSurfacePrincipal{
		Identity: request.Identity, BusinessProfileKey: strings.TrimSpace(request.BusinessProfileKey), BusinessProfileID: strings.TrimSpace(request.BusinessProfileID),
		RequestID: strings.TrimSpace(request.RequestID), CorrelationID: strings.TrimSpace(request.CorrelationID), AuthorizationRevision: resolved.AuthorizationRevision,
	}, nil
}

func (h runtimeAuditApplicationHost) AuthorizeAuditRecord(ctx context.Context, principal auditmodulehost.AuditSurfacePrincipal, objectKey, recordID string) error {
	resolved, err := h.runtimePrincipal(ctx, principal)
	if err != nil {
		return err
	}
	return h.recordAccess.AuthorizeAuditRecord(ctx, resolved, strings.TrimSpace(objectKey), strings.TrimSpace(recordID))
}

func (h runtimeAuditApplicationHost) ProjectAuditEvents(ctx context.Context, principal auditmodulehost.AuditSurfacePrincipal, events []contract.Event) ([]contract.Event, error) {
	resolved, err := h.runtimePrincipal(ctx, principal)
	if err != nil {
		return nil, err
	}
	return h.recordAccess.ProjectAuditEvents(ctx, events, resolved)
}

func (h runtimeAuditApplicationHost) AuditExportTokenKey() []byte {
	return append([]byte(nil), h.exportKey...)
}

func (h runtimeAuditApplicationHost) runtimePrincipal(ctx context.Context, principal auditmodulehost.AuditSurfacePrincipal) (principalmodel.Principal, error) {
	base := principalmodel.NewPrincipalFromIdentity(principal.Identity, principal.RequestID)
	base.CorrelationID = principal.CorrelationID
	return h.principals.ResolveBusinessPrincipal(ctx, base, principal.BusinessProfileKey, principal.BusinessProfileID)
}

var _ auditmodulehost.AuditApplicationHost = runtimeAuditApplicationHost{}
