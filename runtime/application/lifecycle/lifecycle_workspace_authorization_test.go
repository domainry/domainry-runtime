package lifecycle

import (
	"context"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	lifecyclerepository "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/repository"
)

type lifecycleRepositoryCallProbe struct {
	lifecyclerepository.LifecycleRepository
	getLegalHoldCalls int
}

func (repository *lifecycleRepositoryCallProbe) GetLegalHold(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error) {
	repository.getLegalHoldCalls++
	return lifecyclemodel.LegalHold{}, false, nil
}

func TestLifecycleApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	repository := &lifecycleRepositoryCallProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})

	_, err := service.EndLegalHold(
		t.Context(),
		"workspace-b",
		"hold-1",
		"legal",
		"case-closed",
		time.Now().UTC(),
		lifecycleAdmin("workspace-a", "admin-a"),
	)
	if err == nil {
		t.Fatal("cross-workspace lifecycle request was authorized")
	}
	if repository.getLegalHoldCalls != 0 {
		t.Fatalf("repository was called before workspace authorization: calls=%d", repository.getLegalHoldCalls)
	}
}
