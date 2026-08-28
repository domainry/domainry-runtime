package policy

import (
	"fmt"
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// CapabilityValidateRule evaluates a capability_available validation rule against the provided record data
// and capability context. Returns a validation error if the capability is not satisfied.
func CapabilityValidateRule(capabilityKind string, config map[string]any, data map[string]any) error {
	switch capabilitycontract.CapabilityKind(strings.TrimSpace(capabilityKind)) {
	case capabilitycontract.CapabilityInventory:
		return validateInventoryCapability(config, data)
	case capabilitycontract.CapabilityPromotion:
		return validatePromotionCapability(config, data)
	case capabilitycontract.CapabilityApproval:
		return validateApprovalCapability(config, data)
	case capabilitycontract.CapabilityPricing:
		// Pricing availability is always OK at validation time; calculation happens in action operations.
		return nil
	case capabilitycontract.CapabilityLoyalty:
		// Loyalty granting is always available; balance check happens in action operations.
		return nil
	case capabilitycontract.CapabilityStateMachine:
		return validateStateMachineCapability(config, data)
	default:
		return validationError("backend.capability.kind_unsupported", "kind", strings.TrimSpace(capabilityKind))
	}
}

func validateInventoryCapability(config map[string]any, data map[string]any) error {
	quantityField, _ := stringConfig(config, "quantity_field")
	if quantityField == "" {
		quantityField = "quantity"
	}
	stockField, _ := stringConfig(config, "stock_field")
	if stockField == "" {
		stockField = "stock"
	}
	requested, hasQuantity := numericAny(data[quantityField])
	available, hasStock := numericAny(data[stockField])
	if hasQuantity && hasStock && requested > available {
		return validationError("backend.capability.inventory_insufficient",
			"requested", fmt.Sprintf("%.4g", requested),
			"available", fmt.Sprintf("%.4g", available))
	}
	return nil
}

func validatePromotionCapability(config map[string]any, data map[string]any) error {
	activeField, _ := stringConfig(config, "active_field")
	if activeField == "" {
		activeField = "is_active"
	}
	if active, ok := boolAny(data[activeField]); ok && !active {
		return validationError("backend.capability.promotion_inactive")
	}
	return nil
}

func validateApprovalCapability(config map[string]any, data map[string]any) error {
	statusField, _ := stringConfig(config, "status_field")
	if statusField == "" {
		statusField = "approval_status"
	}
	requiredStatus, _ := stringConfig(config, "required_status")
	if requiredStatus == "" {
		requiredStatus = "approved"
	}
	value, exists := data[statusField]
	if !exists || value == nil {
		return nil
	}
	current := strings.TrimSpace(fmt.Sprint(value))
	if current != "" && current != requiredStatus {
		return validationError("backend.capability.approval_required",
			"required", requiredStatus, "current", current)
	}
	return nil
}

func validateStateMachineCapability(config map[string]any, data map[string]any) error {
	stateField, _ := stringConfig(config, "state_field")
	if stateField == "" {
		stateField = "state"
	}
	allowedTransitions := stringListAny(config["allowed_from"])
	if len(allowedTransitions) == 0 {
		return nil
	}
	value, exists := data[stateField]
	if !exists || value == nil {
		return nil
	}
	current := strings.TrimSpace(fmt.Sprint(value))
	if current != "" && !containsString(allowedTransitions, current) {
		return validationError("backend.capability.state_transition_denied",
			"state", current)
	}
	return nil
}

// CapabilityApplyOperation executes a side-effect operation for a capability.
// Returns a patch to merge into the record data, or an error.
func CapabilityApplyOperation(operation string, config map[string]any, data map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(operation) {
	case "transition_state":
		return applyTransitionState(config, data)
	case "calculate_price":
		return applyCalculatePrice(config, data)
	case "apply_promotion":
		return applyPromotion(config, data)
	case "adjust_numeric":
		return applyAdjustNumeric(config, data)
	case "consume_inventory":
		return applyConsumeInventory(config, data)
	case "aggregate_records":
		// Aggregation result is injected by the service layer; domain only validates config.
		return nil, nil
	case "emit_audit":
		// Audit emission is a side effect handled by the service layer.
		return nil, nil
	case "notify":
		// Notification dispatch is handled by the service layer.
		return nil, nil
	case "validate_condition":
		return applyValidateCondition(config, data)
	default:
		return nil, nil
	}
}

// applyTransitionState performs a guarded state field transition.
// Config keys:
//   - state_field  (default: "state")   — field name to update
//   - to           (required)           — target state value
//   - allowed_from (optional []string)  — allowed source states; empty = any
func applyTransitionState(config map[string]any, data map[string]any) (map[string]any, error) {
	stateField, _ := stringConfig(config, "state_field")
	if stateField == "" {
		stateField = "state"
	}
	toState, _ := stringConfig(config, "to")
	if toState == "" {
		return nil, validationError("backend.capability.transition_state_missing_target", "state_field", stateField)
	}
	current := strings.TrimSpace(fmt.Sprint(data[stateField]))
	allowedFrom := stringListAny(config["allowed_from"])
	if len(allowedFrom) > 0 && !containsString(allowedFrom, current) {
		return nil, validationError("backend.capability.state_transition_denied", "state", current, "to", toState)
	}
	return map[string]any{stateField: toState}, nil
}

func applyCalculatePrice(config map[string]any, data map[string]any) (map[string]any, error) {
	baseField, _ := stringConfig(config, "base_price_field")
	if baseField == "" {
		baseField = "base_price"
	}
	outputField, _ := stringConfig(config, "output_field")
	if outputField == "" {
		outputField = "calculated_price"
	}
	base, ok := numericAny(data[baseField])
	if !ok {
		return nil, nil
	}
	discount := 0.0
	discountField, _ := stringConfig(config, "discount_field")
	if discountField != "" {
		discount, _ = numericAny(data[discountField])
	}
	if discountRate, ok := numericAny(config["discount_rate"]); ok {
		discount += base * discountRate
	}
	final := base - discount
	if final < 0 {
		final = 0
	}
	return map[string]any{outputField: final}, nil
}

func applyPromotion(config map[string]any, data map[string]any) (map[string]any, error) {
	priceField, _ := stringConfig(config, "price_field")
	if priceField == "" {
		priceField = "price"
	}
	outputField, _ := stringConfig(config, "output_field")
	if outputField == "" {
		outputField = "discounted_price"
	}
	price, ok := numericAny(data[priceField])
	if !ok {
		return nil, nil
	}
	discount := 0.0
	if rate, ok := numericAny(config["discount_rate"]); ok {
		discount = price * rate
	}
	if flat, ok := numericAny(config["discount_amount"]); ok {
		discount += flat
	}
	final := price - discount
	if final < 0 {
		final = 0
	}
	return map[string]any{outputField: final}, nil
}

func applyAdjustNumeric(config map[string]any, data map[string]any) (map[string]any, error) {
	targetField, _ := stringConfig(config, "target_field")
	sourceField, _ := stringConfig(config, "source_field")
	if targetField == "" || sourceField == "" {
		return nil, nil
	}
	multiplier, ok := numericAny(config["multiplier"])
	if !ok {
		multiplier = 1.0
	}
	source, ok := numericAny(data[sourceField])
	if !ok {
		return nil, nil
	}
	current, _ := numericAny(data[targetField])
	return map[string]any{targetField: current + source*multiplier}, nil
}

func applyConsumeInventory(config map[string]any, data map[string]any) (map[string]any, error) {
	stockField, _ := stringConfig(config, "stock_field")
	if stockField == "" {
		stockField = "stock"
	}
	quantityField, _ := stringConfig(config, "quantity_field")
	if quantityField == "" {
		quantityField = "quantity"
	}
	stock, ok := numericAny(data[stockField])
	if !ok {
		return nil, nil
	}
	qty, ok := numericAny(data[quantityField])
	if !ok {
		return nil, nil
	}
	remaining := stock - qty
	if remaining < 0 {
		return nil, validationError("backend.capability.inventory_insufficient",
			"requested", fmt.Sprintf("%.4g", qty),
			"available", fmt.Sprintf("%.4g", stock))
	}
	return map[string]any{stockField: remaining}, nil
}

func applyValidateCondition(config map[string]any, data map[string]any) (map[string]any, error) {
	fieldKey, _ := stringConfig(config, "field")
	if fieldKey == "" {
		return nil, nil
	}
	expectedValues := stringListAny(config["expected_values"])
	if len(expectedValues) == 0 {
		return nil, nil
	}
	actual := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
	if !containsString(expectedValues, actual) {
		code, _ := stringConfig(config, "error_code")
		if code == "" {
			code = "backend.capability.condition_not_met"
		}
		return nil, validationError(code, "field", fieldKey, "value", actual)
	}
	return nil, nil
}
