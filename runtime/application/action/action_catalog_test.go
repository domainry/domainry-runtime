package action

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type catalogHandler struct {
	descriptor runtimeext.HandlerDescriptor
	invoked    bool
	identity   runtimeext.ExecutionIdentity
}

type mutationHandler struct {
	descriptor runtimeext.HandlerDescriptor
	receipt    *runtimeext.DurableIntentReceipt
}

const (
	actionTestInputType  = "example.com/domainry-project/actions.BookingReserveInput"
	actionTestOutputType = "example.com/domainry-project/actions.BookingReserveOutput"
	actionTestInputHash  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	actionTestOutputHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func actionTestPublishedContract(action definitionmodel.ActionSchema) definitionmodel.ActionSchema {
	action.InputType = actionTestInputType
	action.OutputType = actionTestOutputType
	action.InputContractSHA256 = actionTestInputHash
	action.OutputContractSHA256 = actionTestOutputHash
	return action
}

func actionTestHandlerIdentity() runtimeext.HandlerDescriptor {
	return runtimeext.HandlerDescriptor{
		InputType: actionTestInputType, OutputType: actionTestOutputType,
		InputContractSHA256: actionTestInputHash, OutputContractSHA256: actionTestOutputHash, HandlerRevision: "handler-v1",
	}
}

func actionTestHandlerDescriptor(key string, objects []runtimeext.ActionObjectCapability, connectors ...runtimeext.ActionConnectorCapability) runtimeext.HandlerDescriptor {
	descriptor := actionTestHandlerIdentity()
	descriptor.ActionKey = key
	descriptor.ObjectCapabilities = objects
	descriptor.ConnectorCapabilities = connectors
	return descriptor
}

func newActionTestBusinessHandlerExecutor(dependencies BusinessHandlerExecutionDependencies) *BusinessHandlerExecutor {
	dependencies.RuntimeRevision = "runtime-test"
	dependencies.ProjectRevision = "project-test"
	dependencies.ApplicationSchemaRevision = "snapshot-test"
	return NewBusinessHandlerExecutor(dependencies)
}

func (h mutationHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }
func (h mutationHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "booking", Fields: map[string]any{"status": "created"}}); err != nil {
		return nil, err
	}
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationUpdate, ObjectKey: "class", RecordID: "class-1", Fields: map[string]any{"remaining": 9}}); err != nil {
		return nil, err
	}
	receipt, err := execution.StageDurableIntent(ctx, runtimeext.DurableIntent{ConsumerKey: "email", ConnectionKey: "primary", OperationKey: "booking.confirmed", ContractSHA256: strings.Repeat("c", 64), Payload: map[string]any{"booking_id": "booking-1"}})
	if err != nil {
		return nil, err
	}
	if h.receipt != nil {
		*h.receipt = receipt
	}
	return json.RawMessage(`{"booking_id":"booking-1"}`), nil
}

func (h *catalogHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }
func (h *catalogHandler) Invoke(_ context.Context, execution runtimeext.ActionExecution, input json.RawMessage) (json.RawMessage, error) {
	h.identity = execution.Identity()
	h.invoked = len(input) > 0
	return json.RawMessage(`{"accepted":true}`), nil
}

func TestActionCatalogRejectsMutableBusinessHandlerRegistry(t *testing.T) {
	registry := runtimeext.NewBusinessHandlerRegistry()
	catalog := NewActionCatalog(nil, NewSystemOperationCatalog(), registry)
	if errors := catalog.ValidationErrors(); len(errors) != 1 || errors[0].Error() != "business handler registry must be frozen before Action Catalog validation" {
		t.Fatalf("mutable registry validation errors=%v", errors)
	}
}

func TestBusinessHandlerExecutorRequiresCompleteTraceIdentity(t *testing.T) {
	errors := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).ValidationErrors()
	want := []string{
		"business handler Runtime revision is required",
		"business handler Project revision is required",
		"business handler Metadata revision resolver is required",
	}
	if len(errors) != len(want) {
		t.Fatalf("validation errors=%v", errors)
	}
	for index := range want {
		if errors[index].Error() != want[index] {
			t.Fatalf("validation errors=%v", errors)
		}
	}
	if errors := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).ValidationErrors(); len(errors) != 0 {
		t.Fatalf("complete execution identity errors=%v", errors)
	}
}

func TestActionCatalogRequiresGeneratedCredentialOutputWithoutSourceLineage(t *testing.T) {
	descriptor := actionTestHandlerDescriptor("employee.invite", nil)
	descriptor.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
	descriptor.IdentityHandlerDelivery = &runtimeext.IdentityHandlerDeliveryCapability{
		Operations: []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreate}, InitialCredentialOutputField: "initial_credential",
	}
	handler := &catalogHandler{descriptor: descriptor}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	base := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "employee.invite", ObjectKey: "employee", Kind: definitionmodel.ActionKindObjectOperation,
		TargetOrganization: &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation},
	})
	for _, test := range []struct {
		name   string
		fields []definitionmodel.ActionOutputField
		want   string
	}{
		{name: "missing", want: "requires generated initial credential output field"},
		{name: "scalar", fields: []definitionmodel.ActionOutputField{{Key: "initial_credential", Type: "text"}}, want: "must be a generated non-repeated object"},
		{name: "lineage", fields: []definitionmodel.ActionOutputField{{Key: "initial_credential", Type: "object", SourceObjectKey: "employee", SourceFieldKey: "credential"}}, want: "must be a generated non-repeated object"},
		{name: "repeated", fields: []definitionmodel.ActionOutputField{{Key: "initial_credential", Type: "object", Repeated: true}}, want: "must be a generated non-repeated object"},
		{name: "valid", fields: []definitionmodel.ActionOutputField{{Key: "initial_credential", Type: "object"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			action := base
			action.OutputFields = test.fields
			errors := NewActionCatalog([]definitionmodel.ActionSchema{action}, NewSystemOperationCatalog(), registry).ValidationErrors()
			if test.want == "" {
				if len(errors) != 0 {
					t.Fatalf("errors=%v", errors)
				}
				return
			}
			if len(errors) != 1 || !strings.Contains(errors[0].Error(), test.want) {
				t.Fatalf("errors=%v", errors)
			}
		})
	}
}

func TestActionCatalogRequiresExactStoreOrganizationMutationGrant(t *testing.T) {
	descriptor := actionTestHandlerDescriptor("store.settings.replace", nil)
	descriptor.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
	descriptor.StoreOrganizationMutation = &runtimeext.ActionStoreOrganizationMutationCapability{Operations: []runtimeext.StoreOrganizationMutationOperation{runtimeext.StoreOrganizationMutationRename}}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(&catalogHandler{descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	base := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "store.settings.replace", ObjectKey: "store_settings", Kind: definitionmodel.ActionKindRecordUpdate,
		TargetOrganization: &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceRecordOwner},
	})
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 1 || !strings.Contains(errors[0].Error(), "store organization mutation capability mismatch") {
		t.Fatalf("missing policy errors=%v", errors)
	}
	base.StoreOrganizationMutation = &definitionmodel.ActionStoreOrganizationMutationPolicy{Operations: []string{"disable"}}
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 1 || !strings.Contains(errors[0].Error(), "store organization mutation capability mismatch") {
		t.Fatalf("wrong operation errors=%v", errors)
	}
	base.StoreOrganizationMutation.Operations = []string{"rename"}
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 0 {
		t.Fatalf("matching policy errors=%v", errors)
	}
}

func TestActionCatalogRequiresExactOrganizationUnitDeliveryGrant(t *testing.T) {
	descriptor := actionTestHandlerDescriptor("department_profile.provision", nil)
	descriptor.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit}
	descriptor.OrganizationUnitDelivery = &runtimeext.OrganizationUnitDeliveryCapability{
		Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
		NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
		ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
	}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(&catalogHandler{descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	base := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "department_profile.provision", ObjectKey: "department_profile", Kind: definitionmodel.ActionKindObjectOperation,
		TargetOrganization: &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceDeliveredOrganizationUnit},
	})
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 1 || !strings.Contains(errors[0].Error(), "organization unit delivery capability mismatch") {
		t.Fatalf("missing manifest policy errors=%v", errors)
	}
	base.OrganizationUnitDelivery = &definitionmodel.ActionOrganizationUnitDeliveryPolicy{Operations: []string{"create"}, NodeTypes: []string{"team"}, ParentSource: "workspace_company"}
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 1 || !strings.Contains(errors[0].Error(), "organization unit delivery capability mismatch") {
		t.Fatalf("wrong manifest node type errors=%v", errors)
	}
	base.OrganizationUnitDelivery.NodeTypes = []string{"department"}
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 0 {
		t.Fatalf("matching manifest policy errors=%v", errors)
	}
}

func TestActionCatalogRejectsEmptyAndDuplicatePublishedActionKeys(t *testing.T) {
	actions := []definitionmodel.ActionSchema{
		{Key: " ", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectCreate},
		{Key: "booking.create", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectCreate},
		{Key: " booking.create ", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectCreate},
	}
	errors := NewActionCatalog(actions, NewRuntimeSystemOperationCatalog(), frozenEmptyHandlerRegistry(t)).ValidationErrors()
	want := []string{"action at index 0 has an empty key", "action key booking.create is duplicated"}
	if len(errors) != len(want) {
		t.Fatalf("validation errors=%v", errors)
	}
	for index := range want {
		if errors[index].Error() != want[index] {
			t.Fatalf("validation errors=%v", errors)
		}
	}
}

func TestActionCatalogRejectsBusinessHandlerContractMismatch(t *testing.T) {
	registry := runtimeext.NewBusinessHandlerRegistry()
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"update"}}})}
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	base := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation})
	tests := []struct {
		name   string
		mutate func(*definitionmodel.ActionSchema)
		field  string
	}{
		{name: "missing input type", mutate: func(action *definitionmodel.ActionSchema) { action.InputType = "" }, field: "requires input_type"},
		{name: "input type", mutate: func(action *definitionmodel.ActionSchema) { action.InputType += "Changed" }, field: "input_type mismatch"},
		{name: "output type", mutate: func(action *definitionmodel.ActionSchema) { action.OutputType += "Changed" }, field: "output_type mismatch"},
		{name: "input hash", mutate: func(action *definitionmodel.ActionSchema) { action.InputContractSHA256 = strings.Repeat("c", 64) }, field: "input_contract_sha256 mismatch"},
		{name: "output hash", mutate: func(action *definitionmodel.ActionSchema) { action.OutputContractSHA256 = strings.Repeat("d", 64) }, field: "output_contract_sha256 mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := base
			test.mutate(&action)
			errors := NewActionCatalog([]definitionmodel.ActionSchema{action}, NewSystemOperationCatalog(), registry).ValidationErrors()
			if len(errors) != 1 || !strings.Contains(errors[0].Error(), test.field) {
				t.Fatalf("validation errors=%v", errors)
			}
		})
	}
	if errors := NewActionCatalog([]definitionmodel.ActionSchema{base}, NewSystemOperationCatalog(), registry).ValidationErrors(); len(errors) != 0 {
		t.Fatalf("matching Registry/Catalog contract errors=%v", errors)
	}
}

func TestActionCatalogRejectsBusinessContractFieldsOnSystemAction(t *testing.T) {
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.create", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectCreate})
	errors := NewActionCatalog([]definitionmodel.ActionSchema{action}, NewRuntimeSystemOperationCatalog(), frozenEmptyHandlerRegistry(t)).ValidationErrors()
	if len(errors) != 1 || !strings.Contains(errors[0].Error(), "must not declare business handler contract field input_type") {
		t.Fatalf("validation errors=%v", errors)
	}
}

func TestRuntimeSystemOperationCatalogCoversClosedSingleObjectOperations(t *testing.T) {
	handler := func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
		return ActionExecutionResult{}, nil
	}
	catalog := NewRuntimeSystemOperationCatalog()
	executor := NewRuntimeSystemOperationExecutor(catalog, SystemOperationHandlers{
		Create: handler, Update: handler, Delete: handler, Restore: handler, Transition: handler, ConditionalUpdate: handler,
	})
	if errors := catalog.ValidationErrors(); len(errors) != 0 {
		t.Fatalf("valid Runtime System Operation Catalog errors=%v", errors)
	}
	if errors := executor.ValidationErrors(); len(errors) != 0 {
		t.Fatalf("valid Runtime System Operation Executor errors=%v", errors)
	}
	want := map[string]string{
		definitionmodel.ActionKindObjectCreate:      SystemOperationCreate,
		definitionmodel.ActionKindRecordUpdate:      SystemOperationUpdate,
		definitionmodel.ActionKindRecordDelete:      SystemOperationDelete,
		definitionmodel.ActionKindRecordRestore:     SystemOperationRestore,
		definitionmodel.ActionKindTransitionState:   SystemOperationTransition,
		definitionmodel.ActionKindConditionalUpdate: SystemOperationConditionalUpdate,
	}
	if descriptors := catalog.Descriptors(); len(descriptors) != len(want) {
		t.Fatalf("descriptors=%v", descriptors)
	}
	for kind, key := range want {
		descriptor, found, err := catalog.Resolve(definitionmodel.ActionSchema{Key: "object.operation", Kind: kind})
		if err != nil || !found || descriptor.Key != key || descriptor.Kind != kind {
			t.Fatalf("kind=%q descriptor=%+v found=%v error=%v", kind, descriptor, found, err)
		}
	}
	if _, found, err := catalog.Resolve(definitionmodel.ActionSchema{Key: "business.operation", Kind: definitionmodel.ActionKindRecordOperation}); err != nil || found {
		t.Fatalf("business action resolved as System Operation: found=%v error=%v", found, err)
	}
}

func TestSystemOperationCatalogFailsClosedOnInvalidContracts(t *testing.T) {
	tests := []struct {
		name    string
		catalog *SystemOperationCatalog
		minimum int
	}{
		{name: "duplicate key", catalog: NewSystemOperationCatalog(
			SystemOperationDescriptor{Key: "record.same", Kind: "kind_a", WriteOperation: "update"},
			SystemOperationDescriptor{Key: "record.same", Kind: "kind_b", WriteOperation: "update"},
		), minimum: 1},
		{name: "duplicate kind", catalog: NewSystemOperationCatalog(
			SystemOperationDescriptor{Key: "record.a", Kind: "same_kind", WriteOperation: "update"},
			SystemOperationDescriptor{Key: "record.b", Kind: "same_kind", WriteOperation: "update"},
		), minimum: 1},
		{name: "ambiguous selector", catalog: NewSystemOperationCatalog(SystemOperationDescriptor{
			Key: "record.ambiguous", Kind: "kind", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update",
		}), minimum: 1},
		{name: "missing selector", catalog: NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.unmatched", WriteOperation: "update"}), minimum: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if errors := test.catalog.ValidationErrors(); len(errors) < test.minimum {
				t.Fatalf("validation errors=%v, want at least %d", errors, test.minimum)
			}
			if _, _, err := test.catalog.Resolve(definitionmodel.ActionSchema{Key: "object.operation", Kind: "kind"}); err == nil {
				t.Fatal("invalid catalog resolved an Action")
			}
		})
	}
}

func TestActionCatalogRemainingNilAmbiguityAndOwnershipEdges(t *testing.T) {
	var nilSystem *SystemOperationCatalog
	if descriptor, found, err := nilSystem.Resolve(definitionmodel.ActionSchema{}); err != nil || found || descriptor.Key != "" {
		t.Fatalf("nil system resolve descriptor=%+v found=%v err=%v", descriptor, found, err)
	}
	if descriptors := nilSystem.Descriptors(); descriptors != nil {
		t.Fatalf("nil system descriptors=%v", descriptors)
	}

	ambiguous := NewSystemOperationCatalog(
		SystemOperationDescriptor{Key: "custom.a", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update"},
		SystemOperationDescriptor{Key: "custom.b", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update"},
	)
	if _, _, err := ambiguous.Resolve(definitionmodel.ActionSchema{Key: "customer.update"}); err == nil {
		t.Fatal("ambiguous system operation resolved")
	}

	invalidSystem := NewSystemOperationCatalog(SystemOperationDescriptor{})
	catalog := NewActionCatalog([]definitionmodel.ActionSchema{{
		Key: "customer.update", ObjectKey: "customer", Kind: definitionmodel.ActionKindRecordUpdate,
	}}, invalidSystem, nil)
	if entry, found := catalog.Entry("customer.update"); !found || entry.ResolutionError == nil {
		t.Fatalf("invalid system entry=%+v found=%v", entry, found)
	}

	if err := validatePublishedActionContract(ActionCatalogEntry{}); err != nil {
		t.Fatalf("unowned action contract error=%v", err)
	}

	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("customer.create", []runtimeext.ActionObjectCapability{{
		ObjectKey: "customer", Operations: []string{"create"},
	}})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	systemOwned := &ActionCatalog{
		system:   NewRuntimeSystemOperationCatalog(),
		handlers: registry,
		entries: map[string]ActionCatalogEntry{
			"customer.create": {Owner: ActionOwnerSystemOperation},
		},
	}
	if errors := systemOwned.ValidationErrors(); len(errors) != 1 || !strings.Contains(errors[0].Error(), "is not the action owner") {
		t.Fatalf("system-owned handler validation errors=%v", errors)
	}
	brokenBusinessOwner := &ActionCatalog{entries: map[string]ActionCatalogEntry{
		"customer.broken": {Owner: ActionOwnerBusinessHandler, ResolutionError: errors.New("broken binding")},
	}}
	if brokenBusinessOwner.HasBusinessHandlerOwner() {
		t.Fatal("broken business owner counted as executable")
	}

	var nilCatalog *ActionCatalog
	if definitions := nilCatalog.Definitions(); definitions != nil {
		t.Fatalf("nil action definitions=%v", definitions)
	}
	if nilCatalog.HasBusinessHandlerOwner() {
		t.Fatal("nil catalog has business handler owner")
	}
	if errors := nilCatalog.ValidationErrors(); len(errors) != 1 || errors[0].Error() != "action catalog is required" {
		t.Fatalf("nil catalog validation errors=%v", errors)
	}
}

func TestSystemOperationExecutorRequiresExactCatalogBindings(t *testing.T) {
	handler := func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
		return ActionExecutionResult{}, nil
	}
	catalog := NewSystemOperationCatalog(
		SystemOperationDescriptor{Key: "record.update", Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update"},
		SystemOperationDescriptor{Key: "record.delete", Kind: definitionmodel.ActionKindRecordDelete, WriteOperation: "delete"},
	)
	executor := NewSystemOperationExecutor(catalog,
		SystemOperationBinding{Key: "record.update", Handler: handler},
		SystemOperationBinding{Key: "record.orphan", Handler: handler},
	)
	if errors := executor.ValidationErrors(); len(errors) != 2 {
		t.Fatalf("executor validation errors=%v", errors)
	}
	if _, err := executor.execute(t.Context(), governedActionExecution{entry: ActionCatalogEntry{SystemOperation: "record.update"}}); err == nil {
		t.Fatal("invalid executor did not fail closed")
	}
}

func TestActionApplicationReadinessIncludesExecutorBindingErrors(t *testing.T) {
	catalog := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update"})
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog(nil, catalog, nil),
		SystemOperations: NewSystemOperationExecutor(catalog),
	})
	errors := service.CatalogValidationErrors()
	if len(errors) != 2 || errors[0].Error() != "system operation record.update has no handler" || errors[1].Error() != "action unit of work execution store is required" {
		t.Fatalf("readiness errors=%v", errors)
	}
}

func TestActionCatalogFixesExactlyOneOwner(t *testing.T) {
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Matches: func(action definitionmodel.ActionSchema) bool { return action.Kind == "record_update" }, WriteOperation: "update"})
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{
		{ObjectKey: "booking", Operations: []string{"create"}},
		{ObjectKey: "class", Operations: []string{"update"}},
	})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	catalog := NewActionCatalog([]definitionmodel.ActionSchema{
		{Key: "customer.rename", ObjectKey: "customer", Kind: "record_update"},
		actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: "record_operation", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{
			{ObjectKey: "booking", Fields: []string{"status"}},
			{ObjectKey: "class", Fields: []string{"remaining"}},
		}}}),
		{Key: "missing.owner", ObjectKey: "booking", Kind: "record_operation"},
	}, system, registry)
	if entry, _ := catalog.Entry("customer.rename"); entry.Owner != ActionOwnerSystemOperation || entry.SystemOperation != "record.update" || entry.ResolutionError != nil || entry.Definition.EffectSet == nil || len(entry.Definition.EffectSet.Write) != 1 || entry.Definition.EffectSet.Write[0].ObjectKey != "customer" || !reflect.DeepEqual(entry.Definition.EffectSet.Write[0].Operations, []string{"update"}) {
		t.Fatalf("system entry=%+v", entry)
	}
	if entry, _ := catalog.Entry("booking.reserve"); entry.Owner != ActionOwnerBusinessHandler || entry.HandlerBinding.Handler != handler || !reflect.DeepEqual(entry.HandlerBinding.Descriptor, handler.descriptor) || entry.ResolutionError != nil || len(entry.Definition.EffectSet.Write) != 2 {
		t.Fatalf("handler entry=%+v", entry)
	}
	if entry, _ := catalog.Entry("missing.owner"); entry.ResolutionError == nil || entry.Owner != "" {
		t.Fatalf("missing entry=%+v", entry)
	} else if code := apperror.CodeOf(actionOwnerResolutionError(entry)); code != "backend.action.owner_unresolved" {
		t.Fatalf("missing owner code=%q", code)
	}
	if errors := catalog.ValidationErrors(); len(errors) != 1 {
		t.Fatalf("validation errors=%v", errors)
	}
}

func TestActionCatalogUsesPublishedWriteSetToFixOneOwner(t *testing.T) {
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update"})
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{
		{ObjectKey: "booking", Operations: []string{"create"}},
		{ObjectKey: "class", Operations: []string{"update"}},
	})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindRecordUpdate,
		EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{
			{ObjectKey: "class", Operations: []string{"update"}},
			{ObjectKey: "booking", Operations: []string{"create"}},
		}},
	})
	entry, _ := NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry).Entry(action.Key)
	if entry.Owner != ActionOwnerBusinessHandler || entry.SystemOperation != "" || entry.HandlerBinding.Handler != handler || entry.ResolutionError != nil {
		t.Fatalf("multi-object capability owner=%+v", entry)
	}
	if got := []string{entry.Definition.EffectSet.Write[0].ObjectKey, entry.Definition.EffectSet.Write[1].ObjectKey}; !reflect.DeepEqual(got, []string{"booking", "class"}) {
		t.Fatalf("normalized write set=%v", got)
	}
}

func TestActionCatalogDerivesMultiObjectOwnerFromGeneratedHandlerCapability(t *testing.T) {
	system := NewRuntimeSystemOperationCatalog()
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{
		{ObjectKey: "booking", Operations: []string{"create"}},
		{ObjectKey: "class", Operations: []string{"update"}},
	})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectCreate})
	entry, _ := NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry).Entry(action.Key)
	if entry.Owner != ActionOwnerBusinessHandler || entry.ResolutionError != nil || entry.Definition.EffectSet == nil || len(entry.Definition.EffectSet.Write) != 2 {
		t.Fatalf("generated capability owner=%+v", entry)
	}
}

func TestActionCatalogUsesTypedConnectorCapabilityToRequireBusinessOwner(t *testing.T) {
	system := NewRuntimeSystemOperationCatalog()
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("customer.notify", []runtimeext.ActionObjectCapability{{ObjectKey: "customer", Operations: []string{"update"}}}, runtimeext.ActionConnectorCapability{ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send", ContractSHA256: strings.Repeat("c", 64), Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "customer.notify", ObjectKey: "customer", Kind: definitionmodel.ActionKindRecordUpdate})
	entry, _ := NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry).Entry(action.Key)
	if entry.Owner != ActionOwnerBusinessHandler || entry.ResolutionError != nil || entry.HandlerBinding.Handler != handler {
		t.Fatalf("typed Connector capability owner=%+v", entry)
	}
}

func TestActionCatalogRejectsWriteSetCapabilityMismatch(t *testing.T) {
	system := NewRuntimeSystemOperationCatalog()
	action := definitionmodel.ActionSchema{
		Key: "customer.rename", ObjectKey: "customer", Kind: definitionmodel.ActionKindRecordUpdate,
		EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "account", Operations: []string{"update"}}}},
	}
	entry, _ := NewActionCatalog([]definitionmodel.ActionSchema{action}, system, nil).Entry(action.Key)
	if entry.Owner != "" || entry.ResolutionError == nil || !strings.Contains(entry.ResolutionError.Error(), "business write set does not match") {
		t.Fatalf("mismatched capability entry=%+v", entry)
	}
}

func TestActionCatalogRejectsRuntimeOwnerFallback(t *testing.T) {
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update"})
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("customer.rename", []runtimeext.ActionObjectCapability{{ObjectKey: "customer", Operations: []string{"update"}}})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	entry, _ := NewActionCatalog([]definitionmodel.ActionSchema{{Key: "customer.rename", ObjectKey: "customer", Kind: "record_update"}}, system, registry).Entry("customer.rename")
	if entry.ResolutionError == nil || entry.Owner != "" {
		t.Fatalf("duplicate owner must fail closed: %+v", entry)
	}
	catalog := NewActionCatalog(nil, NewSystemOperationCatalog(), registry)
	if errors := catalog.ValidationErrors(); len(errors) != 1 {
		t.Fatalf("orphan handler errors=%v", errors)
	}
}

func TestActionApplicationInvokesFrozenBusinessHandlerRegistry(t *testing.T) {
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"update"}}})}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: "object_operation", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking", Fields: []string{"status"}}}}})
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), UnitOfWork: newActionTestUnitOfWork().manager,
	})
	handler.descriptor = runtimeext.HandlerDescriptor{ActionKey: "booking.mutated", InputContractSHA256: "mutated", OutputContractSHA256: "mutated", HandlerRevision: "mutated"}
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, Input: map[string]any{"booking_id": "booking-1"}, IdempotencyKey: "execution-1", Principal: actionTestPrincipal("booking.reserve")})
	if err != nil || !handler.invoked || handler.identity.ActionKey != action.Key || handler.identity.ExecutionID != "execution-1" || handler.identity.ReceiptID != "execution-1" || handler.identity.RuntimeRevision != "runtime-test" || handler.identity.ProjectRevision != "project-test" || handler.identity.ApplicationSchemaRevision != "snapshot-test" || handler.identity.HandlerRevision != "handler-v1" || result.Object == nil || result.Object.Output["accepted"] != true {
		t.Fatalf("result=%+v invoked=%v identity=%+v error=%v", result, handler.invoked, handler.identity, err)
	}
}

func TestBusinessHandlerExecutorReturnsOneAtomicMutationBatchAndDurableIntent(t *testing.T) {
	newPlan := func(operation, objectKey, recordID string, fields map[string]any) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: "workspace-a", ActorID: "user-a", RoleKey: "operator", Source: transactionmodel.MutationSourceAction, ActionKey: "booking.reserve", CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1"})
		if err != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
		}
		if recordID == "" {
			recordID = objectKey + "-1"
		}
		record := recordmodel.Record{ID: recordID, Data: fields}
		plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: operation, Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record}, nil)
		return plan, record, err
	}
	executor := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
		PlanCreateMutation: func(_ context.Context, objectKey string, fields map[string]any, _ string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			return newPlan("create", objectKey, "", fields)
		},
		PlanUpdateMutation: func(_ context.Context, objectKey, recordID string, fields map[string]any, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			return newPlan("update", objectKey, recordID, fields)
		},
		ValidateDurableIntent: func(context.Context, runtimeext.DurableIntent, principalmodel.Principal) error { return nil },
	})
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: "object_operation", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking", Fields: []string{"status"}}, {ObjectKey: "class", Fields: []string{"remaining"}}}}}
	var receipt runtimeext.DurableIntentReceipt
	handler := mutationHandler{descriptor: actionTestHandlerDescriptor(action.Key, []runtimeext.ActionObjectCapability{
		{ObjectKey: "booking", Operations: []string{"create"}},
		{ObjectKey: "class", Operations: []string{"update"}},
	}, runtimeext.ActionConnectorCapability{ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "booking.confirmed", ContractSHA256: strings.Repeat("c", 64), Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite}), receipt: &receipt}
	result, err := executor.execute(t.Context(), governedActionExecution{
		invocation: actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, Principal: actionTestPrincipal("booking.reserve"), IdempotencyKey: "command-1"},
		entry:      ActionCatalogEntry{Definition: action, HandlerBinding: runtimeext.BusinessHandlerBinding{Descriptor: handler.Descriptor(), Handler: handler}},
		payload:    map[string]any{}, executionID: "execution-1", unitOfWork: newActionTestUnitOfWork(),
	})
	if err != nil || result.Object == nil || len(result.Commits) != 2 || len(result.Commits[1].Outbox) != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	intent := result.Commits[1].Outbox[0]
	if receipt.ID != "durable_intent:execution-1:0" || intent.ID != receipt.ID || intent.ConnectorKey != "email" || intent.ConnectionKey != "primary" || intent.Operation != "booking.confirmed" || intent.DedupKey != "execution-1:intent:0" || intent.RequestFingerprint != strings.Repeat("c", 64) {
		t.Fatalf("durable intent=%+v", intent)
	}
}

func TestBusinessActionPlansConditionalDeleteAndRestoreInOneCommitBoundary(t *testing.T) {
	newPlan := func(operation, objectKey, recordID string) transactionmodel.MutationPlan {
		t.Helper()
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
			WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: "booking.change", CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: operation, Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: recordmodel.Record{ID: recordID}, RecordID: recordID,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	version := int64(3)
	var conditional transactionmodel.ConditionalUpdateInput
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			PlanConditionalUpdate: func(_ context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				conditional = input
				return newPlan("update", objectKey, recordID), recordmodel.Record{ID: recordID}, nil
			},
			PlanDeleteMutation: func(_ context.Context, objectKey, recordID, expectedUpdatedAt string, _ principalmodel.Principal) ([]transactionmodel.MutationPlan, error) {
				if expectedUpdatedAt != "delete-revision" {
					t.Fatalf("delete expected revision = %q", expectedUpdatedAt)
				}
				return []transactionmodel.MutationPlan{newPlan("update", "child", "child-1"), newPlan("delete", objectKey, recordID)}, nil
			},
			PlanRestoreMutation: func(_ context.Context, objectKey, recordID, expectedUpdatedAt string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if expectedUpdatedAt != "restore-revision" {
					t.Fatalf("restore expected revision = %q", expectedUpdatedAt)
				}
				return newPlan("restore", objectKey, recordID), recordmodel.Record{ID: recordID}, nil
			},
		},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}},
		unitOfWork: newActionTestUnitOfWork(),
		action: definitionmodel.ActionSchema{Key: "booking.change", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{
			{ObjectKey: "booking"}, {ObjectKey: "class"},
		}}},
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
		Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "class", RecordID: "class-1", Fields: map[string]any{"remaining": 2},
		ExpectedVersion: &version, ExpectedUpdatedAt: "conditional-revision",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{Operation: runtimeext.MutationDelete, ObjectKey: "booking", RecordID: "booking-1", ExpectedUpdatedAt: "delete-revision"}); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{Operation: runtimeext.MutationRestore, ObjectKey: "booking", RecordID: "booking-2", ExpectedUpdatedAt: "restore-revision"}); err != nil {
		t.Fatal(err)
	}
	if len(conditional.Predicates) != 2 || conditional.Predicates[0].Field != "version" || conditional.Predicates[0].Value != int64(3) || conditional.Predicates[1].Field != "updated_at" {
		t.Fatalf("conditional input = %#v", conditional)
	}
	if len(execution.plans) != 4 || len(execution.updated) != 1 || len(execution.deleted) != 1 || execution.deleted[0].RecordID != "booking-1" || len(execution.restored) != 1 || execution.restored[0].RecordID != "booking-2" {
		t.Fatalf("plans=%d updated=%#v deleted=%#v restored=%#v", len(execution.plans), execution.updated, execution.deleted, execution.restored)
	}
}
