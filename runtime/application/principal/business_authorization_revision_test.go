package principal

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type businessRevisionRecords struct {
	recordrepository.RecordRepository
	rows map[string]recordmodel.Record
}

func (s *businessRevisionRecords) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if row, found := s.rows[object.Key]; found {
		return recordmodel.RecordPageResult{Total: 1, Items: []recordmodel.Record{row}}, nil
	}
	return recordmodel.RecordPageResult{}, nil
}

func TestBusinessResolutionPreservesIdentityRevisionAndInvalidatesBusinessEvidence(t *testing.T) {
	records := &businessRevisionRecords{rows: map[string]recordmodel.Record{}}
	service := NewBusinessPrincipalApplicationService(BusinessPrincipalDependencies{
		Records: records,
		Objects: func() []definitionmodel.ObjectSchema {
			return []definitionmodel.ObjectSchema{{Key: "staff"}, {Key: "vendor"}}
		},
		Extensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{
				{ObjectKey: "staff", IdentityRelationField: "user_id", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "staff"}},
				{ObjectKey: "vendor", IdentityRelationField: "user_id", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "vendor"}},
			}
		},
	})
	base := principalmodel.NewPrincipalFromIdentity(identitysdk.Principal{Known: true, WorkspaceID: "installation", UserID: "admin", AuthorizationRevision: "identity-1"}, "request")
	resolve := func(p principalmodel.Principal, selection string) principalmodel.Principal {
		t.Helper()
		result, err := service.ResolveBusinessPrincipal(t.Context(), p, selection, "")
		if err != nil {
			t.Fatal(err)
		}
		if result.AuthorizationRevision != p.AuthorizationRevision || result.EffectiveAuthorizationRevision() == "" {
			t.Fatalf("corrupted Identity revision: %+v", result)
		}
		return result
	}
	empty := resolve(base, "")
	if resolve(empty, "").EffectiveAuthorizationRevision() != empty.EffectiveAuthorizationRevision() {
		t.Fatal("resolution is not idempotent")
	}
	records.rows["staff"] = recordmodel.Record{ID: "staff-1", UpdatedAt: "v1"}
	staff := resolve(base, "")
	if staff.EffectiveAuthorizationRevision() == empty.EffectiveAuthorizationRevision() {
		t.Fatal("new Profile did not invalidate business revision")
	}
	records.rows["vendor"] = recordmodel.Record{ID: "vendor-1", UpdatedAt: "v1"}
	staff = resolve(base, "staff")
	vendor := resolve(base, "vendor")
	if vendor.EffectiveAuthorizationRevision() == staff.EffectiveAuthorizationRevision() {
		t.Fatal("selection did not invalidate business revision")
	}
	row := records.rows["staff"]
	row.UpdatedAt = "v2"
	records.rows["staff"] = row
	if resolve(base, "staff").EffectiveAuthorizationRevision() == staff.EffectiveAuthorizationRevision() {
		t.Fatal("Profile mutation did not invalidate business revision")
	}
	base.AuthorizationRevision = "identity-2"
	if resolve(base, "vendor").EffectiveAuthorizationRevision() == vendor.EffectiveAuthorizationRevision() {
		t.Fatal("Identity revocation did not invalidate business revision")
	}
}
