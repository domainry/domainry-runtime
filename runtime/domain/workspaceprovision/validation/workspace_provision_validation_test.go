package validation

import (
	"errors"
	"strings"
	"testing"

	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

func validRequest() workspaceprovisionmodel.Request {
	return workspaceprovisionmodel.Request{
		RequestID: "request-1", WorkspaceCode: "workspace-one", WorkspaceName: "Workspace One",
		AdminLoginID: "admin@example.com", AdminName: "Administrator",
	}
}

func TestWorkspaceProvisionValidationOwnsCanonicalCodeSyntax(t *testing.T) {
	for _, value := range []string{"ab", "workspace-one", "a" + strings.Repeat("0", 62)} {
		if err := ValidateCanonicalCode(value); err != nil {
			t.Fatalf("valid canonical code %q: %v", value, err)
		}
	}
	for _, value := range []string{"a", "Workspace", "workspace_one", "a" + strings.Repeat("0", 63)} {
		if err := ValidateCanonicalCode(value); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
			t.Fatalf("invalid canonical code %q: %v", value, err)
		}
	}
	if got := NormalizeCanonicalCode(" Workspace_ONE "); got != "workspace-one" {
		t.Fatalf("normalized canonical code = %q", got)
	}
}

func TestWorkspaceProvisionValidationOwnsWorkspaceRules(t *testing.T) {
	request := NormalizeRequest(validRequest())
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	request.WorkspaceCode = "default"
	if err := ValidateRequest(request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("reserved Workspace code: %v", err)
	}

}
