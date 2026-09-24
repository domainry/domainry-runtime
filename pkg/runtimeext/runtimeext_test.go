package runtimeext

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testBusinessHandler struct {
	descriptor HandlerDescriptor
}

type testWorkspaceBootstrapParticipant struct {
	descriptor WorkspaceBootstrapDescriptor
}

type testAssigneeResolver struct {
	descriptor AssigneeResolverDescriptor
}

func (r testAssigneeResolver) Descriptor() AssigneeResolverDescriptor { return r.descriptor }

func (testAssigneeResolver) Resolve(context.Context, AssigneeResolverCapabilities, AssigneeResolverContext) ([]AssigneeResolverCandidate, error) {
	return []AssigneeResolverCandidate{{UserID: "user-1"}}, nil
}

func validTestAssigneeResolver(key, revision string) testAssigneeResolver {
	descriptor := AssigneeResolverDescriptor{
		ResolverKey: key, ResolverRevision: revision,
		ConfigFields:         []AssigneeResolverConfigField{{Key: "threshold", Type: AssigneeResolverConfigInteger, Required: true}},
		RecordCapabilities:   []AssigneeResolverRecordCapability{{Key: "request", ObjectKey: "request", Fields: []string{"amount", "owner"}, FilterFields: []string{"region"}, MaxRows: 10}},
		RelationCapabilities: []AssigneeResolverRelationCapability{{Key: "members", SourceObjectKey: "request", RelationFieldKey: "project_id", TargetObjectKey: "project_member", TargetFields: []string{"user_id"}, MaxTargets: 20}},
		IdentityProjections:  []string{AssigneeIdentityProjectionFindUser}, MaxReadOperations: 10, MaxCandidates: 20, TimeoutMilliseconds: 500,
	}
	descriptor.ConfigContractSHA256 = descriptor.ComputedConfigContractSHA256()
	return testAssigneeResolver{descriptor: descriptor}
}

func (p testWorkspaceBootstrapParticipant) Descriptor() WorkspaceBootstrapDescriptor {
	return p.descriptor
}

func (testWorkspaceBootstrapParticipant) BuildWorkspaceBootstrap(context.Context, WorkspaceBootstrapContext, map[string]any) ([]WorkspaceBootstrapRecord, error) {
	return nil, nil
}

func (h testBusinessHandler) Descriptor() HandlerDescriptor { return h.descriptor }

func (h testBusinessHandler) Invoke(context.Context, ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"ok"}`), nil
}

func validTestBusinessHandler(key string) testBusinessHandler {
	return testBusinessHandler{descriptor: HandlerDescriptor{
		ActionKey: key, InputType: "example.com/domainry-project/actions.BookClassInput", OutputType: "example.com/domainry-project/actions.BookClassOutput",
		HandlerRevision:       "handler-revision",
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
	invalidContract.InputType = "not a Go type"
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
	if err := synchronousWrite.Validate(); err != nil {
		t.Fatalf("valid synchronous Connector write grant error = %v", err)
	}
}

func TestRuntimeextContractIdentityIsCurrent(t *testing.T) {
	if ContractVersion != "runtimeext-v48" {
		t.Fatalf("contract version = %q", ContractVersion)
	}
	if got := ComputedContractSHA256(); got != ContractSHA256 {
		t.Fatalf("runtimeext contract hash is stale: got %s, want %s", got, ContractSHA256)
	}
}

func TestRecordOwnershipRemainsRuntimeOwned(t *testing.T) {
	for _, field := range []string{"workspace_id", "owner_org_id", "owner_user_id", " OWNER_ORG_ID "} {
		if (RecordQuery{Operation: QueryList, ObjectKey: "store_config", Filters: []Filter{{Field: field, Operator: "eq", Value: "forged"}}, Limit: 1}).Valid() {
			t.Fatalf("Runtime-owned query filter %q was accepted", field)
		}
		if (RecordMutation{Operation: MutationCreate, ObjectKey: "store_config", Fields: map[string]any{field: "forged"}}).Valid() {
			t.Fatalf("Runtime-owned mutation field %q was accepted", field)
		}
	}
}

func TestConditionalUpdateManyIsBoundedAndCannotAcceptRuntimeOwnership(t *testing.T) {
	valid := ConditionalUpdateManyRequest{
		ObjectKey: "shift", ExpectedCount: 2,
		Filters:       []Filter{{Field: "staff_id", Operator: "in", Values: []any{"staff-1", "staff-2"}}},
		Fields:        map[string]any{"status": "finished", "clock_out": "2026-09-06T23:00:00Z"},
		ExactCoverage: ConditionalUpdateManyExactCoverage{Field: "staff_id", ExpectedValues: []any{"staff-1", "staff-2"}},
	}
	if !valid.Valid() {
		t.Fatal("bounded filter-only set mutation was rejected")
	}
	for _, mutate := range []func(*ConditionalUpdateManyRequest){
		func(request *ConditionalUpdateManyRequest) { request.ExpectedCount = 0 },
		func(request *ConditionalUpdateManyRequest) { request.ExpectedCount = ConditionalUpdateManyMaxSize + 1 },
		func(request *ConditionalUpdateManyRequest) { request.Filters = nil },
		func(request *ConditionalUpdateManyRequest) { request.Fields = nil },
		func(request *ConditionalUpdateManyRequest) { request.ExactCoverage.Field = "" },
		func(request *ConditionalUpdateManyRequest) { request.ExactCoverage.ExpectedValues = []any{"staff-1"} },
		func(request *ConditionalUpdateManyRequest) { request.ExactCoverage.ExpectedValues[0] = nil },
		func(request *ConditionalUpdateManyRequest) {
			request.Filters = []Filter{{Field: "owner_org_id", Operator: "eq", Value: "forged"}}
		},
		func(request *ConditionalUpdateManyRequest) { request.Fields = map[string]any{"owner_org_id": "forged"} },
		func(request *ConditionalUpdateManyRequest) { request.Fields = map[string]any{"workspace_id": "forged"} },
	} {
		request := valid
		mutate(&request)
		if request.Valid() {
			t.Fatalf("invalid set mutation was accepted: %#v", request)
		}
	}
}

func TestWorkspaceBootstrapDescriptorBindsControlledInputConstraints(t *testing.T) {
	minimum, maximum, minLength, maxLength := 0.0, 100.0, 3, 8
	descriptor := WorkspaceBootstrapDescriptor{
		Key: "store_settings", InputType: "nightpos.bootstrap.StoreSettings", ParticipantRevision: "revision-1",
		InputFields: []WorkspaceBootstrapInputField{
			{Key: "mode", Type: WorkspaceBootstrapInputString, Required: true, Default: "retail", Enum: []string{"retail", "restaurant"}},
			{Key: "tax_rate", Type: WorkspaceBootstrapInputNumber, Minimum: &minimum, Maximum: &maximum},
			{Key: "code", Type: WorkspaceBootstrapInputString, MinLength: &minLength, MaxLength: &maxLength, Pattern: `^[A-Z]+$`},
		},
		Records: []WorkspaceBootstrapRecordCapability{{Key: "settings", ObjectKey: "store_settings", Fields: []string{"mode", "tax_rate", "code"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		field WorkspaceBootstrapInputField
		value any
	}{
		{descriptor.InputFields[0], "wholesale"},
		{descriptor.InputFields[1], 101},
		{descriptor.InputFields[2], "bad"},
	} {
		if _, err := NormalizeWorkspaceBootstrapInputValue(test.field, test.value); err == nil {
			t.Fatalf("constraint accepted value %#v for field %#v", test.value, test.field)
		}
	}
	invalidDefault := descriptor
	invalidDefault.InputFields = append([]WorkspaceBootstrapInputField(nil), descriptor.InputFields...)
	invalidDefault.InputFields[0].Default = "wholesale"
	invalidDefault.InputContractSHA256 = invalidDefault.ComputedInputContractSHA256()
	if err := invalidDefault.Validate(); !errors.Is(err, ErrWorkspaceBootstrapContractInvalid) {
		t.Fatalf("invalid default error=%v", err)
	}
	drifted := descriptor
	drifted.InputFields = append([]WorkspaceBootstrapInputField(nil), descriptor.InputFields...)
	drifted.InputFields[0].Enum = []string{"retail"}
	if err := drifted.Validate(); !errors.Is(err, ErrWorkspaceBootstrapContractInvalid) {
		t.Fatalf("input hash drift error=%v", err)
	}
}

func TestStoreOrganizationMutationCapabilityRequiresRecordOwnerAndExposesNoTargetSelector(t *testing.T) {
	descriptor := validTestBusinessHandler("store.settings.replace").Descriptor()
	descriptor.StoreOrganizationMutation = &ActionStoreOrganizationMutationCapability{Operations: []StoreOrganizationMutationOperation{StoreOrganizationMutationRename}}
	if err := descriptor.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("mutation without record owner error=%v", err)
	}
	descriptor.TargetOrganization = &ActionTargetOrganizationCapability{Source: TargetOrganizationSourceRecordOwner}
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{StoreOrganizationProvisionRequest{}, StoreOrganizationRenameRequest{}, StoreOrganizationDisableRequest{}} {
		typeOf := reflect.TypeOf(value)
		for _, forbidden := range []string{"OrganizationID", "ParentOrganizationID", "OwnerOrganizationID", "WorkspaceID"} {
			if _, found := typeOf.FieldByName(forbidden); found {
				t.Fatalf("%s exposes caller-selected %s", typeOf.Name(), forbidden)
			}
		}
	}
}

func TestOrganizationUnitDeliveryCapabilityClosesTypeParentAndTarget(t *testing.T) {
	base := validTestBusinessHandler("department.provision").Descriptor()
	base.TargetOrganization = &ActionTargetOrganizationCapability{Source: TargetOrganizationSourceDeliveredOrganizationUnit}
	base.OrganizationUnitDelivery = &OrganizationUnitDeliveryCapability{
		Operations:   []OrganizationUnitDeliveryOperation{OrganizationUnitDeliveryCreate},
		NodeTypes:    []OrganizationUnitNodeType{OrganizationUnitNodeTypeDepartment},
		ParentSource: OrganizationUnitParentSourceWorkspaceCompany,
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HandlerDescriptor){
		func(value *HandlerDescriptor) {
			value.OrganizationUnitDelivery.NodeTypes = []OrganizationUnitNodeType{"store"}
		},
		func(value *HandlerDescriptor) {
			value.OrganizationUnitDelivery.NodeTypes = []OrganizationUnitNodeType{"company"}
		},
		func(value *HandlerDescriptor) {
			value.OrganizationUnitDelivery.Operations = []OrganizationUnitDeliveryOperation{OrganizationUnitDeliveryCreate, OrganizationUnitDeliveryResolve}
		},
		func(value *HandlerDescriptor) {
			value.OrganizationUnitDelivery.ParentSource = OrganizationUnitParentSourceTargetOrganization
		},
		func(value *HandlerDescriptor) {
			value.TargetOrganization = &ActionTargetOrganizationCapability{Source: TargetOrganizationSourceProvisionedStore}
		},
	} {
		candidate := cloneHandlerDescriptor(base)
		mutate(&candidate)
		if err := candidate.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
			t.Fatalf("invalid descriptor accepted: %#v error=%v", candidate, err)
		}
	}
	resolve := cloneHandlerDescriptor(base)
	resolve.TargetOrganization = &ActionTargetOrganizationCapability{Source: TargetOrganizationSourceExplicit, Input: TargetOrganizationInputInvocation}
	resolve.OrganizationUnitDelivery = &OrganizationUnitDeliveryCapability{
		Operations: []OrganizationUnitDeliveryOperation{OrganizationUnitDeliveryResolve},
		NodeTypes:  []OrganizationUnitNodeType{OrganizationUnitNodeTypeDepartment, OrganizationUnitNodeTypeTeam},
	}
	if err := resolve.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{OrganizationUnitDeliveryRequest{}, OrganizationUnitResolveRequest{}} {
		typeOf := reflect.TypeOf(value)
		for _, forbidden := range []string{"WorkspaceID", "AccessToken", "OrganizationID", "ParentOrganizationID", "OwnerOrgID"} {
			if _, found := typeOf.FieldByName(forbidden); found {
				t.Fatalf("%s exposes %s", typeOf.Name(), forbidden)
			}
		}
	}
}

func TestWorkspaceBootstrapIntegerNormalizationNeverRounds(t *testing.T) {
	field := WorkspaceBootstrapInputField{Key: "value", Type: WorkspaceBootstrapInputInteger}
	for _, test := range []struct {
		value any
		want  int64
		ok    bool
	}{
		{value: json.Number("9007199254740993"), want: 9007199254740993, ok: true},
		{value: json.Number("9223372036854775807"), want: math.MaxInt64, ok: true},
		{value: json.Number("9223372036854775808")},
		{value: json.Number("1.5")},
		{value: uint64(math.MaxUint64)},
		{value: float64(9007199254740992)},
	} {
		got, err := NormalizeWorkspaceBootstrapInputValue(field, test.value)
		if test.ok {
			if err != nil || got != test.want {
				t.Fatalf("value=%v got=%#v error=%v", test.value, got, err)
			}
		} else if err == nil {
			t.Fatalf("value=%v was accepted as %#v", test.value, got)
		}
	}
}

func TestWorkspaceBootstrapExactDecimalNormalizationNeverUsesBinaryFloat(t *testing.T) {
	minimum, maximum := 0.0, 1.0
	field := WorkspaceBootstrapInputField{Key: "rate", Type: WorkspaceBootstrapInputExactDecimal, Minimum: &minimum, Maximum: &maximum}
	for _, test := range []struct {
		value any
		ok    bool
	}{
		{value: "0", ok: true},
		{value: "0.10", ok: true},
		{value: "1.000", ok: true},
		{value: "1.01"},
		{value: "01"},
		{value: " 0.1"},
		{value: "1e-1"},
		{value: json.Number("0.1")},
		{value: 0.1},
	} {
		got, err := NormalizeWorkspaceBootstrapInputValue(field, test.value)
		if test.ok {
			if err != nil || got != test.value {
				t.Fatalf("value=%#v got=%#v error=%v", test.value, got, err)
			}
		} else if err == nil {
			t.Fatalf("value=%#v was accepted as %#v", test.value, got)
		}
	}
	descriptor := WorkspaceBootstrapDescriptor{
		Key: "store_settings", InputType: "nightpos.bootstrap.StoreSettings", ParticipantRevision: "revision-1",
		InputFields: []WorkspaceBootstrapInputField{{Key: "rate", Type: WorkspaceBootstrapInputExactDecimal, Required: true, Default: "0.10", Minimum: &minimum, Maximum: &maximum}},
		Records:     []WorkspaceBootstrapRecordCapability{{Key: "settings", ObjectKey: "store_settings", Fields: []string{"rate"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("exact decimal descriptor: %v", err)
	}
}

func TestProjectExtensionRegistryRejectsInvalidDuplicateAndPostFreezeRegistration(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(nil); !errors.Is(err, ErrBusinessHandlerRequired) {
		t.Fatalf("nil handler error = %v", err)
	}
	handler := validTestBusinessHandler("group_class.book_class")
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterBusinessHandler(handler); !errors.Is(err, ErrBusinessHandlerDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	binding, ok := registry.BusinessHandlerBinding(" group_class.book_class ")
	if !ok || binding.Handler == nil || binding.Descriptor.ActionKey != handler.Descriptor().ActionKey {
		t.Fatalf("resolved binding = %#v, %t", binding, ok)
	}
	registry.Freeze()
	if !registry.Frozen() {
		t.Fatal("registry must report frozen")
	}
	if err := registry.RegisterBusinessHandler(validTestBusinessHandler("group_class.cancel_booking")); !errors.Is(err, ErrProjectExtensionRegistryFrozen) {
		t.Fatalf("post-freeze error = %v", err)
	}
}

func TestProjectExtensionRegistrySnapshotsBindingIdentityAtRegistration(t *testing.T) {
	handler := &testBusinessHandler{descriptor: validTestBusinessHandler("group_class.book_class").descriptor}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	handler.descriptor.ActionKey = "group_class.changed_after_registration"
	handler.descriptor.HandlerRevision = "mutated"
	handler.descriptor.ObjectCapabilities[0].ObjectKey = "changed"
	handler.descriptor.ObjectCapabilities[0].Operations[0] = "delete"
	handler.descriptor.ConnectorCapabilities[0].OperationKey = "changed"

	binding, found := registry.BusinessHandlerBinding("group_class.book_class")
	if !found || binding.Handler != handler || binding.Descriptor.ActionKey != "group_class.book_class" || binding.Descriptor.HandlerRevision != "handler-revision" || binding.Descriptor.ObjectCapabilities[0].ObjectKey != "member" || binding.Descriptor.ConnectorCapabilities[0].OperationKey != "send" {
		t.Fatalf("registration-time binding drifted: found=%v binding=%+v", found, binding)
	}
	if _, found := registry.BusinessHandlerBinding("group_class.changed_after_registration"); found {
		t.Fatal("mutable Handler descriptor changed the registered Action key")
	}
	binding.Descriptor.ObjectCapabilities[0].Operations[0] = "restore"
	if descriptors := registry.BusinessHandlerDescriptors(); len(descriptors) != 1 || !reflect.DeepEqual(descriptors[0].ObjectCapabilities[0].Operations, []string{"get", "update"}) {
		t.Fatalf("descriptor snapshot=%+v binding=%+v", descriptors, binding)
	}
}

func TestWorkspaceIdentityUsageGrantIsBoundedAndFrozen(t *testing.T) {
	handler := validTestBusinessHandler("billing.invoice.generate")
	handler.descriptor.WorkspaceIdentityUsage = &WorkspaceIdentityUsageCapability{MaxPageSize: 25}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	handler.descriptor.WorkspaceIdentityUsage.MaxPageSize = 99
	registry.Freeze()
	binding, ok := registry.BusinessHandlerBinding("billing.invoice.generate")
	if !ok || binding.Descriptor.WorkspaceIdentityUsage == nil || binding.Descriptor.WorkspaceIdentityUsage.MaxPageSize != 25 {
		t.Fatalf("binding=%#v", binding)
	}
	binding.Descriptor.WorkspaceIdentityUsage.MaxPageSize = 1
	again, _ := registry.BusinessHandlerBinding("billing.invoice.generate")
	if again.Descriptor.WorkspaceIdentityUsage.MaxPageSize != 25 {
		t.Fatal("Workspace identity usage grant was mutable")
	}
	invalid := validTestBusinessHandler("billing.invalid").Descriptor()
	invalid.WorkspaceIdentityUsage = &WorkspaceIdentityUsageCapability{MaxPageSize: WorkspaceIdentityUsageMaximumPageSize + 1}
	if err := invalid.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("unbounded usage grant error=%v", err)
	}
	if !(WorkspaceIdentityUsageRequest{PageSize: 10, Cursor: "opaque"}).Valid() || (WorkspaceIdentityUsageRequest{PageSize: 101}).Valid() {
		t.Fatal("Workspace identity usage request bounds are invalid")
	}
	if !(WorkspaceIdentityUsageResolveRequest{WorkspaceCode: "night-tokyo", ExpectedWorkspaceRevision: 7}).Valid() ||
		(WorkspaceIdentityUsageResolveRequest{WorkspaceCode: " night-tokyo", ExpectedWorkspaceRevision: 7}).Valid() ||
		(WorkspaceIdentityUsageResolveRequest{WorkspaceCode: "night-tokyo"}).Valid() {
		t.Fatal("Workspace identity usage exact resolve request bounds are invalid")
	}
}

func TestCrossWorkspaceAggregateGrantIsValidatedNormalizedAndFrozen(t *testing.T) {
	handler := validTestBusinessHandler("sales.daily_totals")
	handler.descriptor.CrossWorkspaceAggregates = []CrossWorkspaceAggregateCapability{{
		Key: " daily ", ObjectKey: " sale ",
		Dimensions:    []CrossWorkspaceAggregateDimension{{Key: " store ", Field: CrossWorkspaceDimensionWorkspace}, {Key: " status ", Field: " status "}},
		Measures:      []CrossWorkspaceAggregateMeasure{{Key: " total ", Operation: AggregateSum, Field: " amount "}, {Key: " count ", Operation: AggregateCount}},
		Filters:       []CrossWorkspaceAggregateFilterCapability{{Field: " status ", Operators: []string{" in ", "eq"}}},
		MaxWorkspaces: 10, MaxSourceRows: 1000, MaxResultRows: 100, TimeoutMilliseconds: 500,
	}}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	handler.descriptor.CrossWorkspaceAggregates[0].Dimensions[0].Field = "workspace_id"
	handler.descriptor.CrossWorkspaceAggregates[0].Filters[0].Operators[0] = "sql"
	binding, ok := registry.BusinessHandlerBinding("sales.daily_totals")
	if !ok {
		t.Fatal("frozen binding is missing")
	}
	grant := binding.Descriptor.CrossWorkspaceAggregates[0]
	if grant.Key != "daily" || grant.ObjectKey != "sale" || grant.Dimensions[0].Field != "status" || grant.Dimensions[1].Field != CrossWorkspaceDimensionWorkspace || !reflect.DeepEqual(grant.Filters[0].Operators, []string{"eq", "in"}) {
		t.Fatalf("normalized grant=%#v", grant)
	}
	binding.Descriptor.CrossWorkspaceAggregates[0].Measures[0].Field = "changed"
	again, _ := registry.BusinessHandlerBinding("sales.daily_totals")
	if again.Descriptor.CrossWorkspaceAggregates[0].Measures[0].Field == "changed" {
		t.Fatal("registry aggregate grant was mutable")
	}

	invalid := validTestBusinessHandler("sales.invalid").Descriptor()
	invalid.CrossWorkspaceAggregates = []CrossWorkspaceAggregateCapability{{Key: "leak", ObjectKey: "sale", Measures: []CrossWorkspaceAggregateMeasure{{Key: "value", Operation: AggregateSum, Field: "workspace_id"}}, MaxWorkspaces: 1, MaxSourceRows: 1, MaxResultRows: 1, TimeoutMilliseconds: 1}}
	if err := invalid.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
		t.Fatalf("physical Workspace field was accepted: %v", err)
	}
}

func TestCrossWorkspaceDateBucketGrantIsStaticValidatedAndFrozen(t *testing.T) {
	handler := validTestBusinessHandler("sales.hourly_totals")
	handler.descriptor.CrossWorkspaceAggregates = []CrossWorkspaceAggregateCapability{{
		Key: " hourly ", ObjectKey: " sale ",
		Dimensions:    []CrossWorkspaceAggregateDimension{{Key: " business_hour ", Field: " sold_at ", Transform: &CrossWorkspaceAggregateDimensionTransform{DateBucket: &CrossWorkspaceAggregateDateBucketTransform{Grain: " hour ", TimeZone: " Asia/Tokyo "}}}},
		Measures:      []CrossWorkspaceAggregateMeasure{{Key: "count", Operation: AggregateCount}},
		MaxWorkspaces: 2, MaxSourceRows: 10, MaxResultRows: 10, TimeoutMilliseconds: 100,
	}}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	handler.descriptor.CrossWorkspaceAggregates[0].Dimensions[0].Transform.DateBucket.TimeZone = "Local"
	binding, ok := registry.BusinessHandlerBinding("sales.hourly_totals")
	if !ok {
		t.Fatal("date-bucket binding is missing")
	}
	transform := binding.Descriptor.CrossWorkspaceAggregates[0].Dimensions[0].Transform.DateBucket
	if transform.Grain != "hour" || transform.TimeZone != "Asia/Tokyo" {
		t.Fatalf("normalized transform=%#v", transform)
	}
	transform.Grain = "sql"
	again, _ := registry.BusinessHandlerBinding("sales.hourly_totals")
	if again.Descriptor.CrossWorkspaceAggregates[0].Dimensions[0].Transform.DateBucket.Grain != "hour" {
		t.Fatal("registry date-bucket grant was mutable")
	}

	for _, invalidTransform := range []*CrossWorkspaceAggregateDimensionTransform{
		{DateBucket: &CrossWorkspaceAggregateDateBucketTransform{Grain: "minute", TimeZone: "Asia/Tokyo"}},
		{DateBucket: &CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: "+09:00"}},
		{DateBucket: &CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: "Not/AZone"}},
	} {
		invalid := validTestBusinessHandler("sales.invalid_bucket").Descriptor()
		invalid.CrossWorkspaceAggregates = []CrossWorkspaceAggregateCapability{{Key: "bucket", ObjectKey: "sale", Dimensions: []CrossWorkspaceAggregateDimension{{Key: "hour", Field: "sold_at", Transform: invalidTransform}}, Measures: []CrossWorkspaceAggregateMeasure{{Key: "count", Operation: AggregateCount}}, MaxWorkspaces: 1, MaxSourceRows: 1, MaxResultRows: 1, TimeoutMilliseconds: 1}}
		if err := invalid.Validate(); !errors.Is(err, ErrHandlerCapabilityInvalid) {
			t.Fatalf("invalid date bucket was accepted: %#v err=%v", invalidTransform, err)
		}
	}
}

func TestProjectExtensionRegistryProjectExtensionsAndDescriptorsAreDeterministic(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	descriptor := WorkspaceBootstrapDescriptor{
		Key: " store_settings ", InputType: "example.com/project.StoreSettingsInput", ParticipantRevision: " revision-1 ",
		InputFields: []WorkspaceBootstrapInputField{{Key: " locale ", Type: WorkspaceBootstrapInputString, Enum: []string{"ja-JP", "en-US"}}},
		Records:     []WorkspaceBootstrapRecordCapability{{Key: " settings ", ObjectKey: " store_settings ", Fields: []string{"timezone", "locale"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	participant := testWorkspaceBootstrapParticipant{descriptor: descriptor}
	set := ProjectExtensions{BusinessHandlers: []BusinessHandler{
		validTestBusinessHandler("group_class.cancel_booking"),
		validTestBusinessHandler("group_class.book_class"),
	}, AssigneeResolvers: []AssigneeResolver{validTestAssigneeResolver("finance.approver", "resolver-revision-1")}, WorkspaceBootstrapParticipant: participant}
	if err := registry.RegisterProjectExtensions(set); err != nil {
		t.Fatal(err)
	}
	handlers := registry.BusinessHandlerDescriptors()
	keys := []string{handlers[0].ActionKey, handlers[1].ActionKey}
	if !reflect.DeepEqual(keys, []string{"group_class.book_class", "group_class.cancel_booking"}) {
		t.Fatalf("descriptor keys = %v", keys)
	}
	descriptors := registry.Descriptors()
	identities := make([]string, 0, len(descriptors))
	for _, current := range descriptors {
		identities = append(identities, current.Kind+":"+current.Key)
	}
	if !reflect.DeepEqual(identities, []string{"business_handler:group_class.book_class", "business_handler:group_class.cancel_booking", "workflow_assignee_resolver:finance.approver", "workspace_bootstrap:store_settings"}) {
		t.Fatalf("project extension descriptors = %#v", descriptors)
	}
	descriptors[2].AssigneeResolver.RecordCapabilities[0].Fields[0] = "changed"
	descriptors[3].WorkspaceBootstrap.Records[0].Fields[0] = "changed"
	again := registry.Descriptors()
	if again[2].AssigneeResolver.RecordCapabilities[0].Fields[0] == "changed" || again[3].WorkspaceBootstrap.Records[0].Fields[0] == "changed" {
		t.Fatal("workspace bootstrap descriptor snapshot was mutable")
	}
}

func TestAssigneeResolverDescriptorConfigAndRegistryGovernance(t *testing.T) {
	resolver := validTestAssigneeResolver("finance.approver", "revision-1")
	if err := resolver.descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	if config, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": 10}); err != nil || config["threshold"] != int64(10) {
		t.Fatalf("config=%#v err=%v", config, err)
	}
	if _, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"unknown": true}); !errors.Is(err, ErrAssigneeResolverConfigInvalid) {
		t.Fatalf("unknown config error=%v", err)
	}
	if config, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": float64(10)}); err != nil || config["threshold"] != int64(10) {
		t.Fatalf("JSON integer config=%#v err=%v", config, err)
	}
	if config, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": json.Number("9007199254740993")}); err != nil || config["threshold"] != int64(9007199254740993) {
		t.Fatalf("exact JSON integer config=%#v err=%v", config, err)
	}
	if _, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": 10.5}); !errors.Is(err, ErrAssigneeResolverConfigInvalid) {
		t.Fatalf("fractional integer config error=%v", err)
	}
	if _, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": float64(9007199254740992)}); !errors.Is(err, ErrAssigneeResolverConfigInvalid) {
		t.Fatalf("unsafe binary-float integer config error=%v", err)
	}
	if _, err := NormalizeAssigneeResolverConfig(resolver.descriptor, map[string]any{"threshold": 1, " threshold ": 2}); !errors.Is(err, ErrAssigneeResolverConfigInvalid) {
		t.Fatalf("duplicate normalized config error=%v", err)
	}
	numberDescriptor := resolver.descriptor
	numberDescriptor.ConfigFields = []AssigneeResolverConfigField{{Key: "score", Type: AssigneeResolverConfigNumber}}
	numberDescriptor.ConfigContractSHA256 = numberDescriptor.ComputedConfigContractSHA256()
	if _, err := NormalizeAssigneeResolverConfig(numberDescriptor, map[string]any{"score": math.NaN()}); !errors.Is(err, ErrAssigneeResolverConfigInvalid) {
		t.Fatalf("non-finite number config error=%v", err)
	}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterAssigneeResolver(resolver); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterAssigneeResolver(resolver); !errors.Is(err, ErrAssigneeResolverDuplicate) {
		t.Fatalf("duplicate resolver error=%v", err)
	}
	binding, found := registry.AssigneeResolverBinding(" finance.approver ")
	if !found || binding.Resolver == nil || binding.Descriptor.ResolverRevision != "revision-1" {
		t.Fatalf("binding=%#v found=%v", binding, found)
	}
	registry.Freeze()
	if err := registry.RegisterAssigneeResolver(validTestAssigneeResolver("finance.backup", "revision-1")); !errors.Is(err, ErrProjectExtensionRegistryFrozen) {
		t.Fatalf("post-freeze resolver error=%v", err)
	}
}

func TestProjectExtensionRegistryProjectExtensionsRegistrationIsAtomic(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	first := validTestBusinessHandler("group_class.book_class")
	second := validTestBusinessHandler("group_class.book_class")
	second.descriptor.HandlerRevision = "different-handler-revision"
	err := registry.RegisterProjectExtensions(ProjectExtensions{BusinessHandlers: []BusinessHandler{first, second}})
	if !errors.Is(err, ErrBusinessHandlerDuplicate) {
		t.Fatalf("duplicate extension set error = %v", err)
	}
	if len(registry.BusinessHandlerDescriptors()) != 0 || len(registry.Descriptors()) != 0 {
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

func TestNotificationIntentValidatesGovernedTerminalStates(t *testing.T) {
	base := NotificationIntent{
		EventType: "booking.cancelled", SourceEventID: "booking-1:cancelled", RecipientUserIDs: []string{"member-1"},
		SubjectObjectKey: "booking", SubjectRecordID: "booking-1", SubjectVersion: "v2", DedupeKey: "booking-1:cancelled",
		GroupKey: "booking-1", ActionState: NotificationActionCompleted, AlertState: NotificationAlertResolved,
		OccurredAt: time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC),
	}
	if !base.Valid() {
		t.Fatal("governed terminal notification must be valid")
	}
	invalidAction := base
	invalidAction.ActionState = "reopened"
	if invalidAction.Valid() {
		t.Fatal("unknown action state must be rejected")
	}
	invalidAlert := base
	invalidAlert.AlertState = "closed"
	if invalidAlert.Valid() {
		t.Fatal("unknown alert state must be rejected")
	}
	conflictingLegacyAlert := base
	conflictingLegacyAlert.Alert = true
	if conflictingLegacyAlert.Valid() {
		t.Fatal("legacy firing flag must not conflict with an explicit terminal alert state")
	}
	invalidExpiry := base
	invalidExpiry.ExpiresAt = invalidExpiry.OccurredAt
	if invalidExpiry.Valid() {
		t.Fatal("expiry must be later than occurrence")
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
