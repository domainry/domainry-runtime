package agenthost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestSharedBusinessActionCatalogUsesCurrentReceiptScopeContractAndWholePages(t *testing.T) {
	h, resolver, reads, producer := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	definitions := []definitionmodel.ActionSchema{
		{Key: "customer.register", ObjectKey: "customer", Label: "Create customer", Kind: "object_create", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "name", Type: "text", Required: true}}},
		{Key: "customer.rename", ObjectKey: "customer", Label: "Rename customer", Kind: "record_update", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "name", Type: "text", Required: true}}},
	}
	permissions := []string{"customer.read", "customer.register", "customer.rename", "action.receipt.customer.register.read", "action.receipt.customer.rename.read"}
	attach := func(p principalmodel.Principal, grants []string, scope identity.DataScope) principalmodel.Principal {
		return accessfixture.Attach(p, accessfixture.Bundle{Permissions: grants, DataPolicies: accessfixture.DataPoliciesForPermissions(grants, scope), FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	}
	resolver.principal = attach(resolver.principal, permissions, identity.DataScopeAll)
	newOwner := func(defs []definitionmodel.ActionSchema) *actionapplication.ActionApplicationService {
		handlers := runtimeext.NewProjectExtensionRegistry()
		handlers.Freeze()
		return actionapplication.NewActionApplication(actionapplication.ActionApplicationDependencies{Catalog: actionapplication.NewActionCatalog(defs, actionapplication.NewRuntimeSystemOperationCatalog(), handlers), Authorization: actionapplication.ActionAuthorization{ObjectForAction: func(p principalmodel.Principal, key, op string) (definitionmodel.ObjectSchema, error) {
			if op != "read" || !p.HasExactPermission(key+".read") {
				return definitionmodel.ObjectSchema{}, conversationBusinessError("forbidden")
			}
			return reads.object, nil
		}}})
	}
	h.actions = newOwner(definitions)
	h.schema = appschemaapplication.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{reads.object}, Actions: definitions}}, nil)
	queries := []agent.ConversationBusinessCatalogQuery{
		{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.register"},
		{Kind: "actions", ObjectKey: "customer", Limit: 1},
		{Kind: "actions", ObjectKey: "customer", Limit: 1, After: "customer.register"},
	}
	evidence := []agent.ConversationBusinessEvidence{}
	for _, q := range queries {
		out, err := h.BusinessCatalog(t.Context(), q, producer)
		if err != nil || len(out.Actions) != 1 {
			t.Fatal(out, err)
		}
		evidence = append(evidence, businessReadEvidence(t, h, producer, "business_catalog", q, out))
	}
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	readPermissions := []string{"customer.read", "action.receipt.customer.register.read", "action.receipt.customer.rename.read"}
	principals.actual = attach(principals.actual, readPermissions, identity.DataScopeAll)
	principals.original = attach(principals.original, readPermissions, identity.DataScopeAll)
	for _, e := range evidence {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
			t.Fatal("metadata retained producing action permission", err)
		}
		if err := h.AuthorizeBusinessResultRead(t.Context(), e, producer); err != nil {
			t.Fatal("original user's metadata required producing permission", err)
		}
		bad := e
		bad.Data = json.RawMessage(strings.Replace(string(e.Data), "customer.register", "customer.private", 1))
		if bad.Data != nil && string(bad.Data) != string(e.Data) {
			if err := h.AuthorizeSharedBusinessResultRead(t.Context(), bad, reader, producer); err == nil {
				t.Fatal("forged operation metadata accepted")
			}
		}
		bad = e
		bad.HostProof = "invented"
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), bad, reader, producer); err == nil {
			t.Fatal("unsigned catalog accepted invented proof")
		}
	}
	// Visible first entry cannot authorize the original continuation that
	// reveals another operation beyond this reader's receipt scope.
	principals.actual = attach(principals.actual, readPermissions[:2], identity.DataScopeAll)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence[0], reader, producer); err != nil {
		t.Fatal("permitted exact detail hidden", err)
	}
	for _, e := range evidence[1:] {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
			t.Fatal("whole metadata page bypassed missing next operation scope")
		}
	}
	principals.actual = attach(principals.actual, readPermissions, identity.DataScopeOwner)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence[0], reader, producer); err == nil {
		t.Fatal("self receipt scope authorized another producer contract")
	}
	principals.actual = attach(principals.actual, readPermissions[1:], identity.DataScopeAll)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence[0], reader, producer); err == nil {
		t.Fatal("receipt authorized unreadable object")
	}
	principals.actual = attach(principals.actual, readPermissions, identity.DataScopeAll)
	changed := slices.Clone(definitions)
	changed[0].Label = "Current changed contract"
	h.actions = newOwner(changed)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence[0], reader, producer); err == nil {
		t.Fatal("old catalog ignored current authoritative contract")
	}
}
