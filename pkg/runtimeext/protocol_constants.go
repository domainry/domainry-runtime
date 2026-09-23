package runtimeext

import (
	foundationaction "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// Handler kinds are Runtime protocol values. Project handlers reference these
// names instead of repeating the serialized definition values.
const (
	HandlerKindObjectCreate      = definitionmodel.ActionKindObjectCreate
	HandlerKindObjectOperation   = definitionmodel.ActionKindObjectOperation
	HandlerKindBulkOperation     = definitionmodel.ActionKindBulkOperation
	HandlerKindRecordUpdate      = definitionmodel.ActionKindRecordUpdate
	HandlerKindRecordDelete      = definitionmodel.ActionKindRecordDelete
	HandlerKindRecordRestore     = definitionmodel.ActionKindRecordRestore
	HandlerKindTransitionState   = definitionmodel.ActionKindTransitionState
	HandlerKindConditionalUpdate = definitionmodel.ActionKindConditionalUpdate
	HandlerKindRecordOperation   = definitionmodel.ActionKindRecordOperation
)

// Handler risk levels re-export the canonical Foundation values at the
// project-facing Runtime extension boundary.
const (
	HandlerRiskLow      = string(foundationaction.RiskLow)
	HandlerRiskMedium   = string(foundationaction.RiskMedium)
	HandlerRiskHigh     = string(foundationaction.RiskHigh)
	HandlerRiskCritical = string(foundationaction.RiskCritical)
)

// Object capability operations are the serialized grants accepted by
// HandlerDescriptor.ObjectCapabilities.
const (
	ObjectCapabilityGet                   = string(QueryGet)
	ObjectCapabilityGetForUpdate          = string(QueryGetForUpdate)
	ObjectCapabilityOptional              = "optional"
	ObjectCapabilityList                  = string(QueryList)
	ObjectCapabilityExists                = string(QueryExists)
	ObjectCapabilityCount                 = string(QueryCount)
	ObjectCapabilityCreate                = string(MutationCreate)
	ObjectCapabilityUpdate                = string(MutationUpdate)
	ObjectCapabilityConditionalUpdate     = string(MutationConditionalUpdate)
	ObjectCapabilityConditionalUpdateMany = "conditional_update_many"
	ObjectCapabilityDelete                = string(MutationDelete)
	ObjectCapabilityRestore               = string(MutationRestore)
)

// Standard backend error codes belong to Runtime. Product-specific business
// errors remain project-owned constants.
const (
	ErrorCodeActionInputInvalid             = "backend.action.input_invalid"
	ErrorCodeActionExecutionIdentityInvalid = "backend.action.execution_identity_invalid"
	ErrorCodeActionOutputInvalid            = "backend.action.output_invalid"
	ErrorCodeRecordNotFound                 = "backend.record.not_found"
)
