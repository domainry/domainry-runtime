package runtimeext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

var (
	ErrTypedHandlerRequired           = errors.New("typed business handler is required")
	ErrHandlerCapabilityFactoryNeeded = errors.New("business handler capability factory is required")
)

// CapabilityFactory constructs the metadata-generated, Action-scoped
// capabilities for one invocation. Project code cannot obtain capabilities
// without the Runtime-owned ActionExecution value.
type CapabilityFactory[Capabilities any] func(ActionExecution) (Capabilities, error)

// TypeIdentity returns the stable package-path-qualified identity of a named
// Go type. Handler descriptors use it instead of maintaining handwritten type
// name strings that drift when a package or type is renamed.
func TypeIdentity[T any]() string {
	typeOf := reflect.TypeOf((*T)(nil)).Elem()
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if typeOf.PkgPath() == "" || typeOf.Name() == "" {
		return ""
	}
	return typeOf.PkgPath() + "." + typeOf.Name()
}

// NewTypedBusinessHandler erases a strongly typed project handler into the
// Runtime registry boundary. It centralizes strict JSON decoding, output
// encoding and stable invocation errors while leaving capability construction
// and business behavior in project code.
func NewTypedBusinessHandler[Capabilities, Input, Output any](
	descriptor HandlerDescriptor,
	capabilities CapabilityFactory[Capabilities],
	handler Handler[Capabilities, Input, Output],
) (BusinessHandler, error) {
	descriptor = normalizeHandlerDescriptor(descriptor)
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if capabilities == nil {
		return nil, ErrHandlerCapabilityFactoryNeeded
	}
	if handler == nil {
		return nil, ErrTypedHandlerRequired
	}
	return &typedBusinessHandler[Capabilities, Input, Output]{descriptor: descriptor, capabilities: capabilities, handler: handler}, nil
}

type typedBusinessHandler[Capabilities, Input, Output any] struct {
	descriptor   HandlerDescriptor
	capabilities CapabilityFactory[Capabilities]
	handler      Handler[Capabilities, Input, Output]
}

func (h *typedBusinessHandler[Capabilities, Input, Output]) Descriptor() HandlerDescriptor {
	return cloneHandlerDescriptor(h.descriptor)
}

func (h *typedBusinessHandler[Capabilities, Input, Output]) Invoke(ctx context.Context, execution ActionExecution, payload json.RawMessage) (json.RawMessage, error) {
	var input Input
	if len(bytes.TrimSpace(payload)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, &BusinessError{Code: ErrorCodeActionInputInvalid, Message: err.Error(), Cause: err}
		}
		if err := ensureJSONDocumentEnded(decoder); err != nil {
			return nil, &BusinessError{Code: ErrorCodeActionInputInvalid, Message: err.Error(), Cause: err}
		}
	}
	return h.invoke(ctx, execution, input)
}

func (h *typedBusinessHandler[Capabilities, Input, Output]) InvokeNative(ctx context.Context, execution ActionExecution, value any) (json.RawMessage, bool, error) {
	input, ok := value.(Input)
	if !ok {
		return nil, false, nil
	}
	output, err := h.invoke(ctx, execution, input)
	return output, true, err
}

func (h *typedBusinessHandler[Capabilities, Input, Output]) invoke(ctx context.Context, execution ActionExecution, input Input) (json.RawMessage, error) {
	if execution == nil || strings.TrimSpace(execution.Identity().ActionKey) != h.descriptor.ActionKey {
		return nil, &BusinessError{Code: ErrorCodeActionExecutionIdentityInvalid, Message: "business handler execution identity is invalid"}
	}
	capabilities, err := h.capabilities(execution)
	if err != nil {
		return nil, err
	}
	output, err := h.handler(ctx, capabilities, input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, &BusinessError{Code: ErrorCodeActionOutputInvalid, Message: err.Error(), Cause: err}
	}
	return encoded, nil
}

func ensureJSONDocumentEnded(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
