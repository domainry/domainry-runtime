package party

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type applicationPartyRepository struct {
	value partymodel.Aggregate
}

func (r *applicationPartyRepository) List(context.Context, string) ([]partymodel.Aggregate, error) {
	return []partymodel.Aggregate{r.value}, nil
}
func (r *applicationPartyRepository) Get(context.Context, string, string) (partymodel.Aggregate, bool, error) {
	return r.value, true, nil
}
func (r *applicationPartyRepository) Upsert(_ context.Context, _ string, value partymodel.Aggregate) (partymodel.Aggregate, error) {
	r.value = value
	return value, nil
}

func TestPartyApplicationServiceAuthorizesEveryEntry(t *testing.T) {
	repository := &applicationPartyRepository{value: partymodel.Aggregate{Party: partymodel.Party{ID: "party"}}}
	service := NewPartyApplicationService(partyservice.NewPartyDomainService(repository))
	unknown := principalmodel.Principal{}
	reader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read"}})
	writer := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.write"}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "party.read"}})
	if _, err := service.List(t.Context(), unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown list error=%v", err)
	}
	if _, err := service.List(t.Context(), writer); apperror.CodeOf(err) != "backend.permission.denied" {
		t.Fatalf("writer list error=%v", err)
	}
	if values, err := service.List(t.Context(), reader); err != nil || len(values) != 1 {
		t.Fatalf("list=%#v err=%v", values, err)
	}
	if _, _, err := service.Get(t.Context(), "party", writer); apperror.CodeOf(err) != "backend.permission.denied" {
		t.Fatalf("writer get error=%v", err)
	}
	if value, found, err := service.Get(t.Context(), "party", admin); err != nil || !found || value.Party.ID != "party" {
		t.Fatalf("get=%#v found=%v err=%v", value, found, err)
	}
	item := partymodel.Aggregate{Party: partymodel.Party{ID: "person", Kind: "person", DisplayName: "Person"}, Person: &partymodel.Person{}}
	if _, err := service.Upsert(t.Context(), item, reader); apperror.CodeOf(err) != "backend.permission.denied" {
		t.Fatalf("reader upsert error=%v", err)
	}
	if value, err := service.Upsert(t.Context(), item, writer); err != nil || value.Party.ID != "person" {
		t.Fatalf("upsert=%#v err=%v", value, err)
	}
}
