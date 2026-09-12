package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	businessseed "github.com/domainry/domainry-runtime/runtime/application/seed/business"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type businessSeedIdentityProjection struct {
	runtimeIdentityProjectionStub
	users         map[string]identitysdk.User
	organizations map[string]identitysdk.OrganizationUnit
	requests      []string
	err           error
}

func (projection *businessSeedIdentityProjection) FindUser(ctx context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	projection.requests = append(projection.requests, requestcontext.WorkspaceID(ctx))
	if projection.err != nil {
		return identitysdk.User{}, false, projection.err
	}
	user, found := projection.users[string(lookup.UserID)]
	return user, found, nil
}

func (projection *businessSeedIdentityProjection) FindOrganizationUnit(ctx context.Context, lookup identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	projection.requests = append(projection.requests, requestcontext.WorkspaceID(ctx))
	if projection.err != nil {
		return identitysdk.OrganizationUnit{}, false, projection.err
	}
	organization, found := projection.organizations[lookup.OrgID]
	return organization, found, nil
}

func TestBusinessSeedReferenceResolverSelectsStableWorkspaceScopedIdentityReferences(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "lead", Fields: []definitionmodel.FieldSchema{
			{Key: "company_name", Type: "text", Required: true},
			{Key: "department_id", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"}},
			{Key: "owner_id", Type: "user", Required: true},
		},
	}}}
	projection := &businessSeedIdentityProjection{
		users: map[string]identitysdk.User{
			"admin": {ID: "admin"}, "rep-1": {ID: "rep-1"},
		},
		organizations: map[string]identitysdk.OrganizationUnit{
			"department-1": {ID: "department-1"}, "department-2": {ID: "department-2"},
		},
	}
	candidates := []BusinessSeedReferenceCandidate{
		{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_organization_unit", RecordID: "department-2", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: "other-workspace", TargetObjectKey: "identity_organization_unit", RecordID: "department-0", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_user", RecordID: "rep-1", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_organization_unit", RecordID: "department-1", SourceKind: "runtime_acceptance_fixture"},
		{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_user", RecordID: "admin", SourceKind: "runtime_initial_administrator"},
	}
	resolver := newIdentityBusinessSeedReferenceResolver("workspace-primary", "domainry-runtime", projection, candidates)
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		rows, err := businessseed.BuildManifestBusinessSeedRowsWithReferenceResolver(t.Context(), manifest, "workspace-primary", resolver)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%#v", rows)
		}
		data := map[string]any{}
		if err := json.Unmarshal([]byte(rows[0].DataJSON), &data); err != nil {
			t.Fatal(err)
		}
		if data["department_id"] != "department-1" || data["owner_id"] != "admin" {
			t.Fatalf("generated lead=%#v", data)
		}
		if attempt > 0 && rows[0].DataJSON != previous {
			t.Fatalf("same inputs changed baseline data: first=%s second=%s", previous, rows[0].DataJSON)
		}
		previous = rows[0].DataJSON
	}
	for _, workspaceID := range projection.requests {
		if workspaceID != "workspace-primary" {
			t.Fatalf("unscoped Identity verification request=%q", workspaceID)
		}
	}
}

func TestBusinessSeedReferenceResolverFailsClosedWithStructuredSecretSafeDiagnostics(t *testing.T) {
	const secret = "ActorPassword1!"
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "lead", Fields: []definitionmodel.FieldSchema{{
			Key: "department_id", Type: "relation", Required: true,
			Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"},
		}},
	}}}
	for _, test := range []struct {
		name       string
		projection *businessSeedIdentityProjection
		candidates []BusinessSeedReferenceCandidate
		wantReason string
	}{
		{
			name: "foreign workspace only", projection: &businessSeedIdentityProjection{},
			candidates: []BusinessSeedReferenceCandidate{{WorkspaceID: "foreign", TargetObjectKey: "identity_organization_unit", RecordID: secret, SourceKind: secret}},
			wantReason: "no_scoped_candidate",
		},
		{
			name: "candidate absent from owner projection", projection: &businessSeedIdentityProjection{organizations: map[string]identitysdk.OrganizationUnit{}},
			candidates: []BusinessSeedReferenceCandidate{{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_organization_unit", RecordID: secret, SourceKind: secret}},
			wantReason: "no_verified_candidate",
		},
		{
			name: "owner projection failure", projection: &businessSeedIdentityProjection{err: errors.New(secret)},
			candidates: []BusinessSeedReferenceCandidate{{WorkspaceID: "workspace-primary", TargetObjectKey: "identity_organization_unit", RecordID: "department-1", SourceKind: "runtime_acceptance_fixture"}},
			wantReason: "candidate_verification_failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := newIdentityBusinessSeedReferenceResolver("workspace-primary", "domainry-runtime", test.projection, test.candidates)
			_, err := businessseed.BuildManifestBusinessSeedRowsWithReferenceResolver(t.Context(), manifest, "workspace-primary", resolver)
			var diagnostic *businessseed.BaselineReferenceResolutionError
			if !errors.As(err, &diagnostic) || diagnostic.ObjectKey != "lead" || diagnostic.FieldKey != "department_id" || diagnostic.TargetObjectKey != "identity_organization_unit" || diagnostic.WorkspaceID != "workspace-primary" || diagnostic.Reason != test.wantReason {
				t.Fatalf("diagnostic=%+v error=%v", diagnostic, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("startup diagnostic leaked secret: %v", err)
			}
		})
	}
}

func TestBusinessSeedReferenceResolverDoesNotBypassAuthoredExternalDefaults(t *testing.T) {
	const unsafeDefault = "foreign-department-password-ActorPassword1!"
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "lead", Fields: []definitionmodel.FieldSchema{{
			Key: "department_id", Type: "relation", Required: true, DefaultValue: unsafeDefault,
			Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"},
		}},
	}}}
	projection := &businessSeedIdentityProjection{organizations: map[string]identitysdk.OrganizationUnit{
		"department-1": {ID: "department-1"},
	}}
	resolver := newIdentityBusinessSeedReferenceResolver("workspace-primary", "domainry-runtime", projection, []BusinessSeedReferenceCandidate{{
		WorkspaceID: "workspace-primary", TargetObjectKey: "identity_organization_unit", RecordID: "department-1", SourceKind: "runtime_acceptance_fixture",
	}})
	_, err := businessseed.BuildManifestBusinessSeedRowsWithReferenceResolver(t.Context(), manifest, "workspace-primary", resolver)
	var diagnostic *businessseed.BaselineReferenceResolutionError
	if !errors.As(err, &diagnostic) || diagnostic.Reason != "preferred_candidate_unavailable" || diagnostic.ScopedCount != 1 {
		t.Fatalf("diagnostic=%+v error=%v", diagnostic, err)
	}
	if strings.Contains(err.Error(), unsafeDefault) {
		t.Fatalf("authored external default leaked through startup diagnostic: %v", err)
	}
}
