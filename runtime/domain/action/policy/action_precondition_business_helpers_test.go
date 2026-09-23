package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestActionNextSelectOption(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "status", Config: map[string]any{"options": []any{"draft", "open", "lost"}}},
		{Key: "empty", Config: map[string]any{}},
	}}
	if next, ok := ActionNextSelectOption(object, "status", "draft"); !ok || next != "open" {
		t.Fatalf("next=%q ok=%v", next, ok)
	}
	if next, ok := ActionNextSelectOption(object, "status", "open"); ok || next != "" {
		t.Fatalf("lost transition next=%q ok=%v", next, ok)
	}
	if next, ok := ActionNextSelectOption(object, "status", "lost"); ok || next != "" {
		t.Fatalf("last option next=%q ok=%v", next, ok)
	}
	if next, ok := ActionNextSelectOption(object, "status", ""); !ok || next != "draft" {
		t.Fatalf("first=%q ok=%v", next, ok)
	}
	for _, test := range []struct{ field, current string }{{"status", "unknown"}, {"empty", ""}, {"missing", ""}} {
		if _, ok := ActionNextSelectOption(object, test.field, test.current); ok {
			t.Fatalf("unexpected next for %#v", test)
		}
	}
}

func TestActionMakerCheckerPolicies(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "maker"}}
	if err := ActionValidateMakerChecker(definitionmodel.ActionSchema{}, object, recordmodel.Record{}, principal); err != nil {
		t.Fatalf("disabled=%v", err)
	}
	action := definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}, MakerField: "submitted_by"}}
	blankActor := principal
	blankActor.UserID = ""
	if err := ActionValidateMakerChecker(action, object, recordmodel.Record{}, blankActor); err != nil {
		t.Fatalf("blank actor=%v", err)
	}
	if err := ActionValidateMakerChecker(action, object, recordmodel.Record{Data: map[string]any{"submitted_by": "maker"}}, principal); apperror.CodeOf(err) != "backend.action.maker_checker_denied" {
		t.Fatalf("same maker=%v", err)
	}
	if err := ActionValidateMakerChecker(action, object, recordmodel.Record{Data: map[string]any{"submitted_by": "other"}}, principal); err != nil {
		t.Fatalf("different checker=%v", err)
	}
	if err := ActionValidateMakerChecker(action, object, recordmodel.Record{Data: map[string]any{"submitted_by": "", "owner": "maker"}}, principal); apperror.CodeOf(err) != "backend.action.maker_checker_denied" {
		t.Fatalf("candidate fallback=%v", err)
	}
	if err := ActionValidateMakerChecker(action, object, recordmodel.Record{Data: map[string]any{}}, principal); err != nil {
		t.Fatalf("missing maker should be allowed: %v", err)
	}
	if !makerCheckerEnabled(action) {
		t.Fatal("typed maker-checker method should enable policy")
	}
	if makerCheckerEnabled(definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"other"}}}) {
		t.Fatal("unrelated assurance method enabled maker-checker policy")
	}
	if got := makerFieldCandidates(definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{MakerField: " "}}, definitionmodel.ObjectSchema{}); len(got) != 2 {
		t.Fatalf("blank maker field candidates = %#v", got)
	}
	if got := makerFieldCandidates(definitionmodel.ActionSchema{}, definitionmodel.ObjectSchema{}); len(got) != 2 {
		t.Fatalf("ownerless maker candidates = %#v", got)
	}
}

func TestActionScalarHelpers(t *testing.T) {
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {false, false, true}, {"true", true, true}, {"1", true, true}, {" YES ", true, true}, {"false", false, true}, {"0", false, true}, {"no", false, true}, {"unknown", false, false}, {1, false, false}} {
		got, ok := ActionBoolValue(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("bool %#v=%v,%v", test.value, got, ok)
		}
	}
	if !actionIsEmptyValue(nil) || !actionIsEmptyValue(" ") || actionIsEmptyValue(0) {
		t.Fatal("empty-value classification mismatch")
	}
	if _, ok := actionDateOnly(nil); ok {
		t.Fatal("nil date should be invalid")
	}
	if _, ok := actionDateOnly(""); ok {
		t.Fatal("blank date should be invalid")
	}
	if parsed, ok := actionDateOnly("2026-07-19T12:00:00Z"); !ok || parsed.Day() != 19 {
		t.Fatalf("date=%v ok=%v", parsed, ok)
	}
}

func TestActionOwnerFieldResolution(t *testing.T) {
	identityObject := definitionmodel.ObjectSchema{
		Fields: []definitionmodel.FieldSchema{{Key: "profile", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}},
	}
	if got := actionOwnerFieldKey(identityObject); got != "profile" {
		t.Fatalf("identity owner=%q", got)
	}
	for name, object := range map[string]definitionmodel.ObjectSchema{
		"invalid config":       {Fields: []definitionmodel.FieldSchema{{Key: "profile", Type: "relation", Config: map[string]any{"object_key": true}}}},
		"missing key":          {Fields: []definitionmodel.FieldSchema{{Key: "other", Type: "relation", Config: map[string]any{"object_key": "other"}}}},
		"wrong relation type":  {Fields: []definitionmodel.FieldSchema{{Key: "profile", Type: "text", Config: map[string]any{"object_key": "identity_user"}}}},
		"wrong preferred type": {Fields: []definitionmodel.FieldSchema{{Key: "assignee", Type: "text"}}},
		"no user field":        {Fields: []definitionmodel.FieldSchema{{Key: "description", Type: "text"}}},
	} {
		if got := actionOwnerFieldKey(object); got != "" {
			t.Fatalf("%s owner=%q", name, got)
		}
	}
	preferred := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "other", Type: "user"}, {Key: "assignee", Type: "user"}}}
	if got := actionOwnerFieldKey(preferred); got != "assignee" {
		t.Fatalf("preferred owner=%q", got)
	}
	fallback := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "reviewer", Type: "user"}}}
	if got := actionOwnerFieldKey(fallback); got != "reviewer" {
		t.Fatalf("fallback owner=%q", got)
	}
	if got := actionOwnerFieldKey(definitionmodel.ObjectSchema{}); got != "" {
		t.Fatalf("empty owner=%q", got)
	}
}

func TestActionCollectionHelpers(t *testing.T) {
	values := ActionUniqueNonEmptyStrings([]string{" one ", "", "one", "two"})
	if len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Fatalf("values=%#v", values)
	}
	original := map[string]any{"one": 1}
	clone := ActionCloneData(original)
	clone["one"] = 2
	if original["one"] != 1 || clone["one"] != 2 {
		t.Fatalf("original=%#v clone=%#v", original, clone)
	}
}
