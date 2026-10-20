package agenthost

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilemodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type businessProjectionTestSchema struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s *businessProjectionTestSchema) ForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

func copyProjectionPrincipal(t *testing.T, p principalmodel.Principal) principalmodel.Principal {
	t.Helper()
	raw, err := json.Marshal(p.AccessBundle)
	if err != nil {
		t.Fatal(err)
	}
	p.AccessBundle = new(identitysdk.AccessBundle)
	if err := json.Unmarshal(raw, p.AccessBundle); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBusinessProjectionReusesOnlyExactCurrentPolicyAndSchema(t *testing.T) {
	h, _, _, a := newConversationBusinessFixture(t)
	p, err := h.principal(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	schema := &businessProjectionTestSchema{snapshot: h.schema.ForPrincipal(t.Context(), p)}
	h.schema = schema
	first := h.businessProjection(t.Context(), p)
	if got := h.businessProjection(t.Context(), p); got != first {
		t.Fatal("identical policy/schema repeated projection")
	}
	if first.policyDigest != businessPolicyDigest(p, schema.snapshot.Objects) || first.readDigest != businessReadPolicyDigest(p, readableBusinessObjects(schema.snapshot, p)) {
		t.Fatal("memo changed original evidence digest semantics")
	}

	for name, mutate := range map[string]func(*principalmodel.Principal){
		"same revision field revocation": func(p *principalmodel.Principal) {
			for i := range p.AccessBundle.FieldPolicies {
				if p.AccessBundle.FieldPolicies[i].Field == "name" {
					p.AccessBundle.FieldPolicies[i].Read = false
				}
			}
		},
		"same revision function revocation": func(p *principalmodel.Principal) { p.AccessBundle.FunctionGrants[0].Effect = identitysdk.EffectDeny },
		"same revision data scope": func(p *principalmodel.Principal) {
			p.AccessBundle.DataPolicies[0].DataScopes = []identitysdk.DataScope{identitysdk.DataScopeAll}
		},
		"revision":       func(p *principalmodel.Principal) { p.AccessBundle.AuthorizationRevision += "-changed" },
		"bundle subject": func(p *principalmodel.Principal) { p.AccessBundle.Subject.SubjectID = "another-subject" },
		"workspace":      func(p *principalmodel.Principal) { p.WorkspaceID = "another-workspace" },
		"role":           func(p *principalmodel.Principal) { p.RoleKey = "another-role" },
		"system scope": func(p *principalmodel.Principal) {
			p.SystemScope = principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "projection-test")
		},
		"system capabilities": func(p *principalmodel.Principal) { p.SystemCapabilities = []string{"customer.read"} },
		"business claims": func(p *principalmodel.Principal) {
			p.BusinessClaims = map[string]profilemodel.ClaimValue{"industry": {Value: "manufacturing"}}
		},
		"evaluation instant": func(p *principalmodel.Principal) { p.AuthorizationEvaluatedAt = time.Now().UTC() },
	} {
		t.Run(name, func(t *testing.T) {
			changed := copyProjectionPrincipal(t, p)
			mutate(&changed)
			if got := h.businessProjection(t.Context(), changed); got == first {
				t.Fatal("changed authority reused prior projection")
			}
		})
	}

	// The owner publishes a new canonical schema hash when a field changes,
	// even when Identity revision and all policy contents remain identical.
	schema.snapshot.Objects = append([]definitionmodel.ObjectSchema(nil), schema.snapshot.Objects...)
	schema.snapshot.Objects[0].Fields = append([]definitionmodel.FieldSchema(nil), schema.snapshot.Objects[0].Fields...)
	schema.snapshot.Objects[0].Fields[0].Sensitive = true
	schema.snapshot.SchemaHash = appschemaservice.SchemaSnapshotHash(schema.snapshot)
	if got := h.businessProjection(t.Context(), p); got == first || got.readDigest == first.readDigest {
		t.Fatal("changed schema reused prior projection or policy digest")
	}
}

func TestBusinessProjectionExpiryCancellationAndBound(t *testing.T) {
	h, _, _, a := newConversationBusinessFixture(t)
	p, err := h.principal(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	schema := &businessProjectionTestSchema{snapshot: h.schema.ForPrincipal(t.Context(), p)}
	h.schema = schema
	first := h.businessProjection(t.Context(), p)
	refreshed := copyProjectionPrincipal(t, p)
	refreshed.AccessBundle.ExpiresAt = p.AccessBundle.ExpiresAt.Add(time.Hour)
	if h.businessProjection(t.Context(), refreshed) != first {
		t.Fatal("equivalent refresh repeated pure computation")
	}
	first.expiresAt = time.Now().Add(-time.Second)
	if h.businessProjection(t.Context(), refreshed) == first {
		t.Fatal("expired entry retained")
	}
	expired := copyProjectionPrincipal(t, p)
	expired.AccessBundle.ExpiresAt = time.Now().Add(-time.Second)
	if _, _, ok := businessProjectionKey(schema.snapshot, expired, time.Now()); ok {
		t.Fatal("expired authority is cacheable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := h.businessProjection(ctx, refreshed); got == h.businessProjection(t.Context(), refreshed) {
		t.Fatal("cancelled evaluation reused cached projection")
	}
	schema.snapshot.SchemaHash = ""
	if _, _, ok := businessProjectionKey(schema.snapshot, p, time.Now()); ok {
		t.Fatal("unversioned schema is cacheable")
	}
	schema.snapshot.SchemaHash = appschemaservice.SchemaSnapshotHash(schema.snapshot)
	for i := 0; i < businessPolicyProjectionLimit+3; i++ {
		changed := p
		changed.RoleKey = fmt.Sprintf("role-%d", i)
		h.businessProjection(t.Context(), changed)
		if len(h.projections) > businessPolicyProjectionLimit {
			t.Fatal("unbounded projection cache")
		}
	}
}

func TestBusinessProjectionConcurrentReadersShareImmutableComputation(t *testing.T) {
	h, _, _, a := newConversationBusinessFixture(t)
	p, err := h.principal(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	h.schema = &businessProjectionTestSchema{snapshot: h.schema.ForPrincipal(t.Context(), p)}
	var wg sync.WaitGroup
	entries := make([]*businessPolicyProjection, 16)
	for i := range entries {
		wg.Go(func() { entries[i] = h.businessProjection(t.Context(), p) })
	}
	wg.Wait()
	for _, entry := range entries {
		if entry != entries[0] {
			t.Fatal("concurrent miss repeated projection")
		}
	}
}

func TestBusinessProjectionNeverMemoizesRowsOrPrincipalAuthorization(t *testing.T) {
	h, resolver, reads, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}
	row, err := h.GetBusinessRecord(t.Context(), q, a)
	if err != nil || string(row.Data["name"]) != `"Alpha"` {
		t.Fatal(row, err)
	}
	e := sealedBusinessEvidence(t, h, a, "get_record", q, row)
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	p, err := h.principal(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	projection := h.businessProjection(t.Context(), p)
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = "another-user" })
	if h.businessProjection(t.Context(), p) != projection {
		t.Fatal("row change should not invalidate pure metadata computation")
	}
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("warm metadata cache concealed row ownership transfer")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = a.UserID })
	resolver.revoke()
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("warm metadata cache concealed principal revocation")
	}
}
