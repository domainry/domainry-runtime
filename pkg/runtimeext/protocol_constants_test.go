package runtimeext

import "testing"

type protocolConstantsInput struct{}

func TestExportedObjectCapabilityConstantsValidateAsOneDescriptor(t *testing.T) {
	descriptor := HandlerDescriptor{
		ActionKey:       "example.run",
		InputType:       "example.RunInput",
		OutputType:      "example.RunOutput",
		HandlerRevision: "example-run-v1",
		ObjectCapabilities: []ActionObjectCapability{
			{
				ObjectKey: "example",
				Operations: []string{
					ObjectCapabilityGet,
					ObjectCapabilityGetForUpdate,
					ObjectCapabilityOptional,
					ObjectCapabilityList,
					ObjectCapabilityExists,
					ObjectCapabilityCount,
					ObjectCapabilityCreate,
					ObjectCapabilityUpdate,
					ObjectCapabilityConditionalUpdate,
					ObjectCapabilityConditionalUpdateMany,
					ObjectCapabilityDelete,
					ObjectCapabilityRestore,
				},
			},
		},
	}
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("exported capability constants must be accepted by HandlerDescriptor: %v", err)
	}
}

func TestExportedHandlerProtocolConstantsAreAvailableAtProjectBoundary(t *testing.T) {
	values := []string{
		HandlerKindObjectCreate,
		HandlerKindObjectOperation,
		HandlerKindBulkOperation,
		HandlerKindRecordUpdate,
		HandlerKindRecordDelete,
		HandlerKindRecordRestore,
		HandlerKindTransitionState,
		HandlerKindConditionalUpdate,
		HandlerKindRecordOperation,
		HandlerRiskLow,
		HandlerRiskMedium,
		HandlerRiskHigh,
		HandlerRiskCritical,
		ErrorCodeActionInputInvalid,
		ErrorCodeActionExecutionIdentityInvalid,
		ErrorCodeActionOutputInvalid,
		ErrorCodeRecordNotFound,
	}
	for index, value := range values {
		if value == "" {
			t.Fatalf("protocol constant %d is blank", index)
		}
	}
}

func TestTypeIdentityUsesTheNamedGoType(t *testing.T) {
	want := "github.com/domainry/domainry-runtime/pkg/runtimeext.protocolConstantsInput"
	if got := TypeIdentity[protocolConstantsInput](); got != want {
		t.Fatalf("TypeIdentity[protocolConstantsInput]()=%q want %q", got, want)
	}
	if got := TypeIdentity[*protocolConstantsInput](); got != want {
		t.Fatalf("TypeIdentity[*protocolConstantsInput]()=%q want %q", got, want)
	}
	if got := TypeIdentity[struct{}](); got != "" {
		t.Fatalf("unnamed TypeIdentity=%q want blank", got)
	}
}
