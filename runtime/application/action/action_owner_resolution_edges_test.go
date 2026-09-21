package action

import (
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func actionOwnerEffect(read, write []definitionmodel.ActionObjectEffect) *definitionmodel.ActionEffectSet {
	return &definitionmodel.ActionEffectSet{Read: read, Write: write}
}

func actionOwnerBinding(capabilities ...runtimeext.ActionObjectCapability) runtimeext.BusinessHandlerBinding {
	return runtimeext.BusinessHandlerBinding{Descriptor: runtimeext.HandlerDescriptor{ObjectCapabilities: capabilities}}
}

func TestResolvePublishedActionOwnerEdges(t *testing.T) {
	definition := definitionmodel.ActionSchema{Key: "order.update", ObjectKey: "order"}
	system := SystemOperationDescriptor{Key: "record.update", WriteOperation: "update"}
	sameHandler := actionOwnerBinding(runtimeext.ActionObjectCapability{ObjectKey: "order", Operations: []string{"update"}})

	invalid := definition
	invalid.EffectSet = actionOwnerEffect(nil, []definitionmodel.ActionObjectEffect{{ObjectKey: "", Operations: []string{"update"}}})
	if _, _, _, _, err := resolvePublishedActionOwner(invalid, system, true, runtimeext.BusinessHandlerBinding{}, false); err == nil {
		t.Fatal("invalid published effect set accepted")
	}
	if _, owner, operation, _, err := resolvePublishedActionOwner(definition, system, true, runtimeext.BusinessHandlerBinding{}, false); err != nil || owner != ActionOwnerSystemOperation || operation != system.Key {
		t.Fatalf("system owner=%q operation=%q error=%v", owner, operation, err)
	}
	if _, owner, _, _, err := resolvePublishedActionOwner(definition, SystemOperationDescriptor{}, false, sameHandler, true); err != nil || owner != ActionOwnerBusinessHandler {
		t.Fatalf("handler owner=%q error=%v", owner, err)
	}
	if _, _, _, _, err := resolvePublishedActionOwner(definition, system, true, sameHandler, true); err == nil || !strings.Contains(err.Error(), "both system operation") {
		t.Fatalf("ambiguous owner error=%v", err)
	}
	extraRead := actionOwnerBinding(
		runtimeext.ActionObjectCapability{ObjectKey: "order", Operations: []string{"update"}},
		runtimeext.ActionObjectCapability{ObjectKey: "inventory", Operations: []string{"get"}},
	)
	if _, _, _, _, err := resolvePublishedActionOwner(definition, system, true, extraRead, true); err == nil || !strings.Contains(err.Error(), "both system operation") {
		t.Fatalf("system kind with business handler error=%v", err)
	}
	mismatch := definition
	mismatch.EffectSet = actionOwnerEffect(nil, []definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"delete"}}})
	if _, _, _, _, err := resolvePublishedActionOwner(mismatch, system, true, sameHandler, true); err == nil || !strings.Contains(err.Error(), "both system operation") {
		t.Fatalf("mismatch error=%v", err)
	}
	if _, _, _, _, err := resolvePublishedActionOwner(mismatch, SystemOperationDescriptor{}, false, sameHandler, true); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("handler-only mismatch error=%v", err)
	}
	if _, _, _, _, err := resolvePublishedActionOwner(definition, SystemOperationDescriptor{}, false, runtimeext.BusinessHandlerBinding{}, false); err == nil || !strings.Contains(err.Error(), "no published") {
		t.Fatalf("missing owner error=%v", err)
	}
	fileDefinition := definition
	fileDefinition.FileOperations = []string{runtimeext.FileOperationVerifyClean}
	fileBinding := sameHandler
	fileBinding.Descriptor.FileCapabilities = []string{runtimeext.FileOperationVerifyClean}
	if _, owner, _, _, err := resolvePublishedActionOwner(fileDefinition, SystemOperationDescriptor{}, false, fileBinding, true); err != nil || owner != ActionOwnerBusinessHandler {
		t.Fatalf("published file owner=%q err=%v", owner, err)
	}
	fileBinding.Descriptor.FileCapabilities = nil
	if _, _, _, _, err := resolvePublishedActionOwner(fileDefinition, SystemOperationDescriptor{}, false, fileBinding, true); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("missing file grant error=%v", err)
	}
	tampered := sameHandler
	tampered.Descriptor.FileCapabilities = []string{runtimeext.FileOperationVerifyClean}
	if _, _, _, _, err := resolvePublishedActionOwner(definition, SystemOperationDescriptor{}, false, tampered, true); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unpublished file grant error=%v", err)
	}
}

func TestActionOwnerCapabilityDerivationEdges(t *testing.T) {
	for _, operation := range []string{"get", "get_for_update", "optional", "list", "exists", "count"} {
		if !actionReadCapabilityOperation(" " + operation + " ") {
			t.Fatalf("read operation %q rejected", operation)
		}
	}
	if actionReadCapabilityOperation("unknown") {
		t.Fatal("unknown read operation accepted")
	}
	for _, operation := range []string{"create", "update", "conditional_update", "delete", "restore"} {
		if !actionCapabilityWriteOperation(" " + operation + " ") {
			t.Fatalf("write operation %q rejected", operation)
		}
	}
	if actionCapabilityWriteOperation("unknown") {
		t.Fatal("unknown write operation accepted")
	}
	effect := effectSetFromHandlerCapabilities([]runtimeext.ActionObjectCapability{
		{ObjectKey: " order ", Operations: []string{" get ", " update ", "unknown"}},
		{ObjectKey: "audit", Operations: []string{"list"}},
	})
	if len(effect.Read) != 2 || len(effect.Write) != 1 {
		t.Fatalf("derived effect=%+v", effect)
	}
	systemEffect := effectSetFromSystemCapability(" order ", " update ")
	if systemEffect.Write[0].ObjectKey != "order" || systemEffect.Write[0].Operations[0] != "update" {
		t.Fatalf("system effect=%+v", systemEffect)
	}
	if selectedPublishedEffectSet(systemEffect, effect) != systemEffect || selectedPublishedEffectSet(nil, effect) != effect {
		t.Fatal("selected effect set changed")
	}
}

func TestHandlerEffectSetClassifiesConditionalUpdateManyAsReadAndWrite(t *testing.T) {
	effect := effectSetFromHandlerCapabilities([]runtimeext.ActionObjectCapability{
		{ObjectKey: "work_item", Operations: []string{"conditional_update_many"}},
	})
	if len(effect.Read) != 1 || len(effect.Write) != 1 {
		t.Fatalf("conditional update many effect=%+v", effect)
	}
	if !reflect.DeepEqual(effect.Read[0].Operations, []string{"conditional_update_many"}) ||
		!reflect.DeepEqual(effect.Write[0].Operations, []string{"conditional_update_many"}) {
		t.Fatalf("conditional update many operations read=%v write=%v", effect.Read[0].Operations, effect.Write[0].Operations)
	}
}

func TestNormalizePublishedEffectSetRejectsMalformedCapabilities(t *testing.T) {
	for _, value := range []*definitionmodel.ActionEffectSet{
		actionOwnerEffect([]definitionmodel.ActionObjectEffect{{ObjectKey: ""}}, nil),
		actionOwnerEffect([]definitionmodel.ActionObjectEffect{{ObjectKey: "order"}, {ObjectKey: " order "}}, nil),
		actionOwnerEffect([]definitionmodel.ActionObjectEffect{{ObjectKey: "order", Fields: []string{""}}}, nil),
		actionOwnerEffect([]definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"get", " get "}}}, nil),
		actionOwnerEffect([]definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"update"}}}, nil),
		actionOwnerEffect(nil, []definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"get"}}}),
	} {
		if _, err := normalizePublishedEffectSet(value); err == nil {
			t.Fatalf("malformed effect accepted: %+v", value)
		}
	}
	normalized, err := normalizePublishedEffectSet(actionOwnerEffect(
		[]definitionmodel.ActionObjectEffect{
			{ObjectKey: "z", Fields: []string{"b", "a"}, Operations: []string{"list"}},
			{ObjectKey: "a", Operations: []string{"get"}},
		},
		[]definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"update"}}},
	))
	if err != nil || normalized.Read[0].ObjectKey != "a" || normalized.Read[1].Fields[0] != "a" {
		t.Fatalf("normalized=%+v error=%v", normalized, err)
	}
	if normalized, err := normalizePublishedEffectSet(nil); err != nil || normalized != nil {
		t.Fatalf("nil normalized=%+v error=%v", normalized, err)
	}
}

func TestActionOwnerEffectComparisonEdges(t *testing.T) {
	base := actionOwnerEffect(nil, []definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"update"}}})
	if effectCapabilityMatches(nil, base) || effectCapabilityMatches(base, nil) {
		t.Fatal("nil effect sets matched")
	}
	if effectCapabilityListMatches(base.Write, nil) {
		t.Fatal("different list lengths matched")
	}
	if effectCapabilityListMatches(base.Write, []definitionmodel.ActionObjectEffect{{ObjectKey: "other", Operations: []string{"update"}}}) {
		t.Fatal("different object matched")
	}
	if effectCapabilityListMatches(base.Write, []definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"delete"}}}) {
		t.Fatal("different operations matched")
	}
	withoutOperations := []definitionmodel.ActionObjectEffect{{ObjectKey: "order"}}
	if !effectCapabilityListMatches(withoutOperations, base.Write) {
		t.Fatal("published object-only capability did not match")
	}
	if sameStrings([]string{"a"}, []string{"a", "b"}) || sameStrings([]string{"a"}, []string{"b"}) || !sameStrings([]string{"a"}, []string{"a"}) {
		t.Fatal("string comparison changed")
	}
}
