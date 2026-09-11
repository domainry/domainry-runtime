package policy

import (
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRecordAuthorizationDelegatesHumanDecisionsToSDKBundle(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "user"},
		{Key: "name", Type: "text"},
		{Key: "email", Type: "email"},
	}}
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-a"}},
		accessfixture.Bundle{
			Key: "operator", Permissions: []string{"case.create", "case.read", "case.update", "case.export"},
			DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "case", Scope: "all", Read: true, Write: true}},
			FieldPolicies: []accessfixture.FieldPolicyFixture{
				{ObjectKey: "case", FieldKey: "name", Read: true, Write: true, Export: true},
				{ObjectKey: "case", FieldKey: "email", Read: true, Export: true, Masked: true},
			},
		},
	)
	record := recordmodel.Record{ID: "case-1", Data: map[string]any{"id": "case-1", "owner": "other", "name": "Example", "email": "person@example.test"}}

	if !RecordAllowsObjectAction(principal, "case", "create") || !RecordAllowsObjectAction(principal, "case", "read") || !RecordAllowsObjectAction(principal, "case", "update") || RecordAllowsObjectAction(principal, "case", "delete") {
		t.Fatal("SDK object decisions were not preserved")
	}
	if scopes := RecordDataScopesForPrincipal(principal, "case", "update"); !reflect.DeepEqual(scopes, []identitysdk.DataScope{identitysdk.DataScopeAll}) {
		t.Fatalf("canonical permission scopes=%v", scopes)
	}
	if !RecordCanAccess(principal, object, record) || !RecordCanWriteScope(principal, object, record.Data) {
		t.Fatal("SDK all-record policy was not used for record access")
	}
	filtered := RecordFilterReadable(principal, object, record)
	if filtered.Data["name"] != "Example" || filtered.Data["email"] != "****@example.test" {
		t.Fatalf("SDK field projection=%#v", filtered.Data)
	}
	if filtered.Data["owner"] != "other" {
		t.Fatalf("ordinary field did not inherit the Identity object grant: %#v", filtered.Data)
	}
	if err := RecordValidateWritableFields(principal, object, map[string]any{"name": "Changed"}); err != nil {
		t.Fatalf("SDK writable field rejected: %v", err)
	}
	if err := RecordValidateWritableFields(principal, object, map[string]any{"email": "changed@example.test"}); err == nil {
		t.Fatal("SDK read-only field accepted for write")
	}
	if got := RecordExportFieldKeys(RecordExportableFieldsForPrincipal(principal, object)); !reflect.DeepEqual(got, []string{"owner", "name", "email"}) {
		t.Fatalf("export fields=%v", got)
	}
	if got := RecordExportMaskedFieldKeysForPrincipal(principal, object); !reflect.DeepEqual(got, []string{"email"}) {
		t.Fatalf("masked export fields=%v", got)
	}
}

func TestRecordAuthorizationSuppliesCanonicalBusinessFactsToSDK(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "submitted_by", Type: "user"}}}
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-a"}},
		accessfixture.Bundle{
			Permissions:  []string{"case.read", "case.update"},
			DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "case", Scope: "owner", Read: true, Write: true}},
		},
	)
	if !RecordCanAccess(principal, object, recordmodel.Record{OwnerUserID: "user-1"}) {
		t.Fatal("canonical owner fact was not supplied to SDK")
	}
	if RecordCanAccess(principal, object, recordmodel.Record{OwnerUserID: "other"}) {
		t.Fatal("SDK owned-record policy allowed another owner")
	}
}

func TestRecordAuthorizationAllowsOnlyExplicitRuntimeSystemAuthorityWithoutBundle(t *testing.T) {
	unknown := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	if RecordAllowsObjectAction(unknown, "case", "read") || RecordCanAccess(unknown, definitionmodel.ObjectSchema{Key: "case"}, recordmodel.Record{}) {
		t.Fatal("human principal without AccessBundle was authorized")
	}
	system := principalmodel.NewSystemPrincipal("runtime-worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "record worker"), "case.read")
	if !RecordAllowsObjectAction(system, "case", "read") || !RecordCanAccess(system, definitionmodel.ObjectSchema{Key: "case"}, recordmodel.Record{}) {
		t.Fatal("explicit Runtime system capability was denied")
	}
}

func TestRecordStaticFieldEnvelopeDelegatesRulesAndGuardrailsToSDK(t *testing.T) {
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-a"}},
		accessfixture.Bundle{
			Permissions: []string{"case.read", "case.update", "case.export"},
			FieldPolicies: []accessfixture.FieldPolicyFixture{
				{
					ObjectKey: "case", FieldKey: "email", Read: true, Write: true, Export: true,
					Policies: []accessfixture.FieldRuleFixture{{Key: "mask-email", Priority: 100, Actions: []string{"read", "export"}, Effect: "mask"}},
				},
				{
					ObjectKey: "case", FieldKey: "amount", Read: false,
					Policies: []accessfixture.FieldRuleFixture{{
						Key: "owner-only", Priority: 100, Actions: []string{"read"}, Effect: "allow",
						Predicate: &accessfixture.PredicateFixture{Operator: "equal", FieldKey: "owner_id", Values: []string{"user-1"}},
					}},
				},
			},
			Guardrails: []accessfixture.GuardrailFixture{{
				Key:               "legal-hold",
				FieldRestrictions: []accessfixture.FieldRestrictionFixture{{ObjectKey: "case", FieldKey: "email", Actions: []string{"update"}, Reason: "legal hold"}},
			}},
		},
	)

	if allowed, masked, handled := RecordSDKReadableField(principal, "case", "email"); !handled || !allowed || !masked {
		t.Fatalf("unconditional SDK mask decision allowed=%v masked=%v handled=%v", allowed, masked, handled)
	}
	if allowed, _, handled := RecordSDKWritableField(principal, "case", "email"); !handled || allowed {
		t.Fatalf("SDK guardrail was bypassed allowed=%v handled=%v", allowed, handled)
	}
	if allowed, masked, handled := RecordSDKExportableField(principal, "case", "email"); !handled || !allowed || !masked {
		t.Fatalf("SDK export mask decision allowed=%v masked=%v handled=%v", allowed, masked, handled)
	}
	if allowed, _, handled := RecordSDKReadableField(principal, "case", "amount"); !handled || allowed {
		t.Fatalf("contextual rule must fail closed without record facts allowed=%v handled=%v", allowed, handled)
	}
	if !RecordFieldRequiresPolicyEvaluation(principal, "case", "amount", "read") {
		t.Fatal("contextual field rule was not disclosed to query planners")
	}
}

func TestSensitiveFieldIsClosedUnlessAnExplicitFieldPolicyNamesIt(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "staff_account", Fields: []definitionmodel.FieldSchema{
		{Key: "display_name", Type: "text"},
		{Key: "pin_fingerprint", Type: "text", Sensitive: true},
	}}
	fingerprint := object.Fields[1]
	record := recordmodel.Record{ID: "staff-1", Data: map[string]any{"id": "staff-1", "display_name": "Doctor", "pin_fingerprint": "1bcd47"}}
	bundle := func(fieldPolicies ...accessfixture.FieldPolicyFixture) principalmodel.Principal {
		return accessfixture.Attach(
			principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-a"}},
			accessfixture.Bundle{
				Key: "front_desk", Permissions: []string{"staff_account.read", "staff_account.update", "staff_account.export"},
				DataPolicies:  []accessfixture.DataPolicyFixture{{ObjectKey: "staff_account", Scope: "all", Read: true, Write: true}},
				FieldPolicies: fieldPolicies,
			},
		)
	}

	// The object-level grant alone opens every ordinary field and no sensitive one.
	inherited := bundle()
	if !RecordCanReadObjectFieldForPrincipal(inherited, object, object.Fields[0]) {
		t.Fatal("ordinary field lost the inherited object grant")
	}
	if RecordCanReadObjectFieldForPrincipal(inherited, object, fingerprint) || RecordCanWriteObjectFieldForPrincipal(inherited, object, fingerprint) || RecordCanExportObjectFieldForPrincipal(inherited, object, fingerprint) {
		t.Fatal("sensitive field inherited the object grant")
	}
	if !RecordCanReadObjectFieldKeyForPrincipal(inherited, object, "display_name") || RecordCanReadObjectFieldKeyForPrincipal(inherited, object, "pin_fingerprint") || RecordCanWriteObjectFieldKeyForPrincipal(inherited, object, "pin_fingerprint") {
		t.Fatal("key lookup did not apply the field's sensitivity")
	}
	if filtered := RecordFilterReadable(inherited, object, record); filtered.Data["display_name"] != "Doctor" || filtered.Data["pin_fingerprint"] != nil {
		t.Fatalf("read projection published the sensitive field: %#v", filtered.Data)
	}
	if got := RecordExportFieldKeys(RecordExportableFieldsForPrincipal(inherited, object)); !reflect.DeepEqual(got, []string{"display_name"}) {
		t.Fatalf("export fields=%v", got)
	}
	if err := RecordValidateWritableFields(inherited, object, map[string]any{"pin_fingerprint": "changed"}); err == nil {
		t.Fatal("sensitive field accepted for write without an explicit policy")
	}

	// An explicit policy for the field governs it in either direction.
	reopened := bundle(accessfixture.FieldPolicyFixture{ObjectKey: "staff_account", FieldKey: "pin_fingerprint", Read: true, Export: true, Masked: true})
	if !RecordCanReadObjectFieldForPrincipal(reopened, object, fingerprint) || !RecordFieldReadMaskedForPrincipal(reopened, object.Key, fingerprint.Key) {
		t.Fatal("explicit masked read policy did not reopen the sensitive field")
	}
	if RecordCanWriteObjectFieldForPrincipal(reopened, object, fingerprint) {
		t.Fatal("explicit read-only policy opened the sensitive field for write")
	}
	closed := bundle(accessfixture.FieldPolicyFixture{ObjectKey: "staff_account", FieldKey: "pin_fingerprint"})
	if RecordCanReadObjectFieldForPrincipal(closed, object, fingerprint) {
		t.Fatal("explicit closed policy was ignored")
	}

	// A principal without an access bundle cannot name the field and stays closed.
	if RecordSensitiveFieldClosedForPrincipal(principalmodel.Principal{}, object.Key, object.Fields[0]) {
		t.Fatal("ordinary field reported closed")
	}
	if !RecordSensitiveFieldClosedForPrincipal(principalmodel.Principal{}, object.Key, fingerprint) {
		t.Fatal("bundle-less principal reached the sensitive field")
	}
}
