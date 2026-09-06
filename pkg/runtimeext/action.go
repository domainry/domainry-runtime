package runtimeext

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var (
	ErrHandlerKeyRequired       = errors.New("business handler key is required")
	ErrHandlerContractRequired  = errors.New("business handler input/output type identities and contract hashes are required")
	ErrHandlerContractInvalid   = errors.New("business handler contract identity is invalid")
	ErrHandlerRevisionRequired  = errors.New("business handler revision is required")
	ErrHandlerCapabilityInvalid = errors.New("business handler capability is invalid")
)

var (
	handlerContractHashPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	handlerTypeIdentityPattern  = regexp.MustCompile(`^[A-Za-z0-9._~\-/]+\.[A-Za-z_][A-Za-z0-9_]*$`)
	handlerFieldIdentityPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ActionObjectCapability is the generated, Action-scoped object authority
// published with a typed Business Handler wrapper.
type ActionObjectCapability struct {
	ObjectKey  string
	Operations []string
}

const FileOperationVerifyClean = "verify_clean"

// ConnectorOperationMode is the Action-visible delivery boundary of one
// generated Connector operation.
type ConnectorOperationMode string

const (
	ConnectorModeCall           ConnectorOperationMode = "call"
	ConnectorModeEnqueue        ConnectorOperationMode = "enqueue"
	ConnectorModeStartOperation ConnectorOperationMode = "start_operation"
)

// ConnectorOperationEffect classifies whether an operation only observes an
// external system or can change/reserve external state.
type ConnectorOperationEffect string

const (
	ConnectorEffectRead    ConnectorOperationEffect = "read"
	ConnectorEffectReserve ConnectorOperationEffect = "reserve"
	ConnectorEffectWrite   ConnectorOperationEffect = "write"
)

// ActionConnectorCapability is one generated Connector grant fixed to a
// published Connection and operation for an Action Handler.
type ActionConnectorCapability struct {
	ConnectorKey   string
	ConnectionKey  string
	OperationKey   string
	ContractSHA256 string
	Mode           ConnectorOperationMode
	Effect         ConnectorOperationEffect
}

func (c ActionConnectorCapability) Valid() bool {
	if strings.TrimSpace(c.ConnectorKey) == "" || strings.TrimSpace(c.ConnectionKey) == "" || strings.TrimSpace(c.OperationKey) == "" || !handlerContractHashPattern.MatchString(c.ContractSHA256) {
		return false
	}
	switch c.Mode {
	case ConnectorModeCall:
		return c.Effect == ConnectorEffectRead
	case ConnectorModeEnqueue, ConnectorModeStartOperation:
		return c.Effect == ConnectorEffectReserve || c.Effect == ConnectorEffectWrite
	default:
		return false
	}
}

// Handler is the fixed source-owned function shape used by generated project
// code. Capabilities is generated per Action and must not be a project-wide
// service container.
type Handler[Capabilities, Input, Output any] func(context.Context, Capabilities, Input) (Output, error)

// HandlerDescriptor is the Runtime-readable identity of a generated typed
// Handler wrapper.
type HandlerDescriptor struct {
	ActionKey                 string
	InputType                 string
	OutputType                string
	InputContractSHA256       string
	OutputContractSHA256      string
	HandlerRevision           string
	ObjectCapabilities        []ActionObjectCapability
	ConnectorCapabilities     []ActionConnectorCapability
	FileCapabilities          []string
	NotificationEventTypes    []string
	CrossWorkspaceAggregates  []CrossWorkspaceAggregateCapability
	TargetOrganization        *ActionTargetOrganizationCapability
	IdentityHandlerDelivery   *IdentityHandlerDeliveryCapability
	StoreOrganizationCatalog  *StoreOrganizationCatalogCapability
	StoreOrganizationMutation *ActionStoreOrganizationMutationCapability
	WorkspaceIdentityUsage    *WorkspaceIdentityUsageCapability
}

func (d HandlerDescriptor) Validate() error {
	if strings.TrimSpace(d.ActionKey) == "" {
		return ErrHandlerKeyRequired
	}
	inputType, outputType := strings.TrimSpace(d.InputType), strings.TrimSpace(d.OutputType)
	inputHash, outputHash := strings.TrimSpace(d.InputContractSHA256), strings.TrimSpace(d.OutputContractSHA256)
	if inputType == "" || outputType == "" || inputHash == "" || outputHash == "" {
		return ErrHandlerContractRequired
	}
	if !handlerTypeIdentityPattern.MatchString(inputType) || !handlerTypeIdentityPattern.MatchString(outputType) || !handlerContractHashPattern.MatchString(inputHash) || !handlerContractHashPattern.MatchString(outputHash) {
		return ErrHandlerContractInvalid
	}
	if strings.TrimSpace(d.HandlerRevision) == "" {
		return ErrHandlerRevisionRequired
	}
	objects := map[string]bool{}
	allowed := map[string]bool{
		"get": true, "get_for_update": true, "optional": true, "list": true, "exists": true, "count": true,
		"create": true, "update": true, "conditional_update": true, "delete": true, "restore": true,
		"conditional_update_many":            true,
		RecordNotificationRecipientOperation: true,
	}
	for _, capability := range d.ObjectCapabilities {
		objectKey := strings.TrimSpace(capability.ObjectKey)
		if objectKey == "" || objects[objectKey] || len(capability.Operations) == 0 {
			return ErrHandlerCapabilityInvalid
		}
		objects[objectKey] = true
		operations := map[string]bool{}
		for _, raw := range capability.Operations {
			operation := strings.TrimSpace(raw)
			if !allowed[operation] || operations[operation] {
				return ErrHandlerCapabilityInvalid
			}
			operations[operation] = true
		}
	}
	connectors := map[string]bool{}
	connections := map[string]string{}
	for _, capability := range d.ConnectorCapabilities {
		connectorKey := strings.TrimSpace(capability.ConnectorKey)
		connectionKey := strings.TrimSpace(capability.ConnectionKey)
		operationKey := strings.TrimSpace(capability.OperationKey)
		identity := connectorKey + "\x00" + operationKey
		registeredConnection, connectorSeen := connections[connectorKey]
		if !capability.Valid() || connectors[identity] || (connectorSeen && registeredConnection != connectionKey) {
			return ErrHandlerCapabilityInvalid
		}
		connectors[identity] = true
		connections[connectorKey] = connectionKey
	}
	files := map[string]bool{}
	for _, raw := range d.FileCapabilities {
		operation := strings.TrimSpace(raw)
		if operation != FileOperationVerifyClean || files[operation] {
			return ErrHandlerCapabilityInvalid
		}
		files[operation] = true
	}
	notifications := map[string]bool{}
	for _, raw := range d.NotificationEventTypes {
		key := strings.TrimSpace(raw)
		if key == "" || notifications[key] {
			return ErrHandlerCapabilityInvalid
		}
		notifications[key] = true
	}
	aggregates := map[string]bool{}
	for _, capability := range d.CrossWorkspaceAggregates {
		key := strings.TrimSpace(capability.Key)
		if key == "" || aggregates[key] || !capability.Valid() {
			return ErrHandlerCapabilityInvalid
		}
		aggregates[key] = true
	}
	if d.TargetOrganization != nil && !d.TargetOrganization.Valid() {
		return ErrHandlerCapabilityInvalid
	}
	if d.IdentityHandlerDelivery != nil && !d.IdentityHandlerDelivery.Valid() {
		return ErrHandlerCapabilityInvalid
	}
	if d.IdentityHandlerDelivery != nil && handlerDeliveryMutatesIdentity(d.IdentityHandlerDelivery.Operations) && d.TargetOrganization == nil {
		return ErrHandlerCapabilityInvalid
	}
	if d.StoreOrganizationCatalog != nil && !d.StoreOrganizationCatalog.Valid() {
		return ErrHandlerCapabilityInvalid
	}
	if d.StoreOrganizationMutation != nil && (!d.StoreOrganizationMutation.Valid() || d.TargetOrganization == nil || d.TargetOrganization.Source != TargetOrganizationSourceRecordOwner) {
		return ErrHandlerCapabilityInvalid
	}
	if d.WorkspaceIdentityUsage != nil && !d.WorkspaceIdentityUsage.Valid() {
		return ErrHandlerCapabilityInvalid
	}
	return nil
}

func handlerDeliveryMutatesIdentity(operations []IdentityHandlerOperation) bool {
	for _, operation := range operations {
		if operation == IdentityHandlerCreate || operation == IdentityHandlerUpdate || operation == IdentityHandlerDisable {
			return true
		}
	}
	return false
}

// BusinessHandler is implemented by generated wrappers. It is the erased
// Runtime registry boundary; user handlers keep the strongly typed Handler
// shape above.
type BusinessHandler interface {
	Descriptor() HandlerDescriptor
	Invoke(context.Context, ActionExecution, json.RawMessage) (json.RawMessage, error)
}
