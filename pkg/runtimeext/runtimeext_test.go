package runtimeext

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type testBusinessHandler struct {
	descriptor HandlerDescriptor
}

func (h testBusinessHandler) Descriptor() HandlerDescriptor { return h.descriptor }

func (h testBusinessHandler) Invoke(context.Context, ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"ok"}`), nil
}

func validTestBusinessHandler(key string) testBusinessHandler {
	return testBusinessHandler{descriptor: HandlerDescriptor{
		ActionKey: key, InputType: "example.com/domainry-project/actions.BookClassInput", OutputType: "example.com/domainry-project/actions.BookClassOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: "handler-revision",
		ObjectCapabilities:    []ActionObjectCapability{{ObjectKey: "member", Operations: []string{"update", "get"}}},
		ConnectorCapabilities: []ActionConnectorCapability{{ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send", ContractSHA256: strings.Repeat("c", 64), Mode: ConnectorModeEnqueue, Effect: ConnectorEffectWrite}},
	}}
}

func TestHandlerDescriptorRequiresStableIdentityAndContracts(t *testing.T) {
	if err := (HandlerDescriptor{}).Validate(); !errors.Is(err, ErrHandlerKeyRequired) {
		t.Fatalf("empty descriptor error = %v", err)
	}
	missingContract := HandlerDescriptor{ActionKey: "group_class.book_class"}
	if err := missingContract.Validate(); !errors.Is(err, ErrHandlerContractRequired) {
		t.Fatalf("missing contract error = %v", err)
	}
	if err := validTestBusinessHandler("group_class.book_class").Descriptor().Validate(); err != nil {
		t.Fatal(err)
	}
	invalidContract := validTestBusinessHandler("group_class.book_class").Descriptor()
	invalidContract.InputContractSHA256 = "not-a-sha256"
	if err := invalidContract.Validate(); !errors.Is(err, ErrHandlerContractInvalid) {
		t.Fatalf("invalid contract error = %v", err)
	}
	missingRevision := validTestBusinessHandler("group_class.book_class").Descriptor()
	missingRevision.HandlerRevision = ""
	if err := missingRevision.Validate(); !errors.Is(err, ErrHandlerRevisionRequired) {
		t.Fatalf("missing revision error = %v", err)
	}
	invalidCapability := validTestBusinessHandler("group_class.book_class").Descriptor()
	invalidCapability.ObjectCapabilities[0].Operations = []string{"sql"}
	if err := invalidCapability.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("invalid capability error = %v", err)
	}
	invalidConnector := validTestBusinessHandler("group_class.book_class").Descriptor()
	invalidConnector.ConnectorCapabilities = []ActionConnectorCapability{{ConnectorKey: "email", OperationKey: "send"}}
	if err := invalidConnector.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("invalid Connector capability error = %v", err)
	}
	conflictingConnections := validTestBusinessHandler("group_class.book_class").Descriptor()
	conflictingConnections.ConnectorCapabilities = append(conflictingConnections.ConnectorCapabilities, ActionConnectorCapability{ConnectorKey: "email", ConnectionKey: "secondary", OperationKey: "cancel", ContractSHA256: strings.Repeat("d", 64), Mode: ConnectorModeEnqueue, Effect: ConnectorEffectWrite})
	if err := conflictingConnections.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("conflicting Connector Connection error = %v", err)
	}
	synchronousWrite := validTestBusinessHandler("group_class.book_class").Descriptor()
	synchronousWrite.ConnectorCapabilities[0].Mode = ConnectorModeCall
	if err := synchronousWrite.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("synchronous Connector write error = %v", err)
	}
}

func TestRuntimeextContractIdentityIsCurrent(t *testing.T) {
	if ContractVersion != "runtimeext-v19" {
		t.Fatalf("contract version = %q", ContractVersion)
	}
	if got := ComputedContractSHA256(); got != ContractSHA256 {
		t.Fatalf("runtimeext contract hash is stale: got %s, want %s", got, ContractSHA256)
	}
}

func TestBusinessHandlerRegistryRejectsInvalidDuplicateAndPostFreezeRegistration(t *testing.T) {
	registry := NewBusinessHandlerRegistry()
	if err := registry.Register(nil); !errors.Is(err, ErrBusinessHandlerRequired) {
		t.Fatalf("nil handler error = %v", err)
	}
	handler := validTestBusinessHandler("group_class.book_class")
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(handler); !errors.Is(err, ErrBusinessHandlerDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	binding, ok := registry.Binding(" group_class.book_class ")
	if !ok || binding.Handler == nil || binding.Descriptor.ActionKey != handler.Descriptor().ActionKey {
		t.Fatalf("resolved binding = %#v, %t", binding, ok)
	}
	registry.Freeze()
	if !registry.Frozen() {
		t.Fatal("registry must report frozen")
	}
	if err := registry.Register(validTestBusinessHandler("group_class.cancel_booking")); !errors.Is(err, ErrBusinessHandlerRegistryFrozen) {
		t.Fatalf("post-freeze error = %v", err)
	}
}

func TestBusinessHandlerRegistrySnapshotsBindingIdentityAtRegistration(t *testing.T) {
	handler := &testBusinessHandler{descriptor: validTestBusinessHandler("group_class.book_class").descriptor}
	registry := NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	handler.descriptor.ActionKey = "group_class.changed_after_registration"
	handler.descriptor.HandlerRevision = "mutated"
	handler.descriptor.ObjectCapabilities[0].ObjectKey = "changed"
	handler.descriptor.ObjectCapabilities[0].Operations[0] = "delete"
	handler.descriptor.ConnectorCapabilities[0].OperationKey = "changed"

	binding, found := registry.Binding("group_class.book_class")
	if !found || binding.Handler != handler || binding.Descriptor.ActionKey != "group_class.book_class" || binding.Descriptor.HandlerRevision != "handler-revision" || binding.Descriptor.ObjectCapabilities[0].ObjectKey != "member" || binding.Descriptor.ConnectorCapabilities[0].OperationKey != "send" {
		t.Fatalf("registration-time binding drifted: found=%v binding=%+v", found, binding)
	}
	if _, found := registry.Binding("group_class.changed_after_registration"); found {
		t.Fatal("mutable Handler descriptor changed the registered Action key")
	}
	binding.Descriptor.ObjectCapabilities[0].Operations[0] = "restore"
	if descriptors := registry.Descriptors(); len(descriptors) != 1 || !reflect.DeepEqual(descriptors[0].ObjectCapabilities[0].Operations, []string{"get", "update"}) {
		t.Fatalf("descriptor snapshot=%+v binding=%+v", descriptors, binding)
	}
}

func TestBusinessHandlerRegistryExtensionSetAndDescriptorsAreDeterministic(t *testing.T) {
	registry := NewBusinessHandlerRegistry()
	set := ExtensionSet{BusinessHandlers: []BusinessHandler{
		validTestBusinessHandler("group_class.cancel_booking"),
		validTestBusinessHandler("group_class.book_class"),
	}}
	if err := registry.RegisterExtensionSet(set); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	keys := []string{descriptors[0].ActionKey, descriptors[1].ActionKey}
	if !reflect.DeepEqual(keys, []string{"group_class.book_class", "group_class.cancel_booking"}) {
		t.Fatalf("descriptor keys = %v", keys)
	}
}

func TestBusinessHandlerRegistryExtensionSetRegistrationIsAtomic(t *testing.T) {
	registry := NewBusinessHandlerRegistry()
	duplicate := validTestBusinessHandler("group_class.book_class")
	err := registry.RegisterExtensionSet(ExtensionSet{BusinessHandlers: []BusinessHandler{duplicate, duplicate}})
	if !errors.Is(err, ErrBusinessHandlerDuplicate) {
		t.Fatalf("duplicate extension set error = %v", err)
	}
	if len(registry.Descriptors()) != 0 {
		t.Fatal("failed extension set must not partially register handlers")
	}
}

func TestPublicExecutionValuesValidateClosedOperations(t *testing.T) {
	for _, phase := range []ExecutionPhase{ExecutionPhasePrewrite, ExecutionPhaseWriting, ExecutionPhaseCommitted, ExecutionPhaseRolledBack} {
		if !phase.Valid() {
			t.Fatalf("phase %q must be valid", phase)
		}
	}
	if ExecutionPhase("unknown").Valid() {
		t.Fatal("unknown phase must be invalid")
	}
	if !(RecordQuery{Operation: QueryGet, ObjectKey: "member", RecordID: "member-1"}).Valid() {
		t.Fatal("typed get query must be valid")
	}
	if !(RecordQuery{Operation: QueryGetForUpdate, ObjectKey: "member", RecordID: "member-1"}).Valid() {
		t.Fatal("typed GetForUpdate query must be valid")
	}
	if (RecordQuery{Operation: QueryGet, ObjectKey: "member"}).Valid() {
		t.Fatal("get query without record id must be invalid")
	}
	if (RecordQuery{Operation: QueryGet, ObjectKey: "member", RecordID: "member-1", Projection: []string{"name"}}).Valid() {
		t.Fatal("get query must not silently ignore projection")
	}
	if !(RecordQuery{Operation: QueryList, ObjectKey: "member", Limit: 10, AfterID: "member-10"}).Valid() {
		t.Fatal("list query with stable id cursor must be valid")
	}
	if (RecordQuery{Operation: QueryCount, ObjectKey: "member", Projection: []string{"name"}}).Valid() {
		t.Fatal("count query must not silently accept projection")
	}
	if (RecordQuery{Operation: QueryList, ObjectKey: "member", RecordID: "ignored"}).Valid() {
		t.Fatal("list query must not silently ignore record id")
	}
	if !(RecordMutation{Operation: MutationCreate, ObjectKey: "booking"}).Valid() {
		t.Fatal("create mutation must be valid")
	}
	if (RecordMutation{Operation: MutationDelete, ObjectKey: "booking"}).Valid() {
		t.Fatal("record mutation without record id must be invalid")
	}
	if (RecordMutation{Operation: MutationUpdate, ObjectKey: "booking", RecordID: "booking-1", ExpectedVersion: new(int64)}).Valid() {
		t.Fatal("plain update must not silently accept conditional inputs")
	}
	if !(RecordMutation{Operation: MutationConditionalUpdate, ObjectKey: "booking", RecordID: "booking-1", ExpectedUpdatedAt: "revision-1"}).Valid() {
		t.Fatal("conditional update with a compare-and-swap token must be valid")
	}
	if (RecordMutation{Operation: MutationConditionalUpdate, ObjectKey: "booking", RecordID: "booking-1"}).Valid() {
		t.Fatal("empty conditional update must be invalid")
	}
	if (RecordMutation{Operation: MutationDelete, ObjectKey: "booking", RecordID: "booking-1", Fields: map[string]any{"status": "deleted"}}).Valid() {
		t.Fatal("delete must not silently ignore fields")
	}
	intent := DurableIntent{
		ConsumerKey:    "connector/access_control/acme",
		ConnectionKey:  "primary",
		OperationKey:   "grant",
		ContractSHA256: strings.Repeat("a", 64),
	}
	if !intent.Valid() {
		t.Fatal("durable intent must be valid")
	}
	if (DurableIntent{OperationKey: "booking.created"}).Valid() {
		t.Fatal("durable intent without consumer identity must be invalid")
	}
	if (DurableIntent{ConsumerKey: "automation", OperationKey: "booking.created"}).Valid() {
		t.Fatal("durable intent without payload contract identity must be invalid")
	}
}

func TestBusinessErrorUsesStableCodeAndPreservesCause(t *testing.T) {
	cause := errors.New("capacity predicate rejected")
	err := &BusinessError{Code: "gym.class.capacity_full", Cause: cause}
	if !err.Valid() || err.Error() != "gym.class.capacity_full" || !errors.Is(err, cause) {
		t.Fatalf("business error = %#v", err)
	}
}

func TestHandlerTypeRequiresContextFirst(t *testing.T) {
	type capabilities struct{}
	type input struct{}
	type output struct{}
	var handler Handler[capabilities, input, output] = func(context.Context, capabilities, input) (output, error) {
		return output{}, nil
	}
	if handler == nil {
		t.Fatal("typed handler must be assignable")
	}
}
