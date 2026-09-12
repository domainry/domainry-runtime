package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type noProfileRecords struct {
	recordrepository.RecordRepository
}

// Exercise the real resolver before the usage boundary. The old test fixture
// supplied equal revisions directly and never traversed this production path.
func TestWorkspaceUsageAcceptsResolvedBusinessPrincipalAndRejectsStaleIdentity(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{NextCursor: "identity-next"}}
	resolver := &workspaceActiveResolverStub{}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	business := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
		Records: &noProfileRecords{}, Objects: func() []definitionmodel.ObjectSchema { return nil },
		Extensions: func() []profilebindingmodel.Binding { return nil },
	})
	p, err := business.ResolveBusinessPrincipal(t.Context(), execution.invocation.Principal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	execution.invocation.Principal = p
	page, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{PageSize: 10})
	if err != nil {
		t.Fatalf("legitimate installation request denied after business resolution: %v", err)
	}
	if usage.request.Authorization.AuthorizationRevision != "authz-1" || page.NextCursor == "" {
		t.Fatalf("lost Identity authorization or cursor: %+v", page)
	}
	execution.invocation.Principal.BusinessAuthorizationRevision = "business-profile-changed"
	if _, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{PageSize: 10, Cursor: page.NextCursor}); err == nil {
		t.Fatal("old business cursor accepted after Profile changed")
	}
	execution.invocation.Principal.AuthorizationRevision = "identity-revoked"
	if err := execution.validateWorkspaceIdentityUsageRequestIdentity(); err == nil {
		t.Fatal("mismatched Identity authorization was accepted")
	}
}
