package validation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var actionPreconditionFieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

var actionPreconditionRuntimeConstants = map[string]bool{
	"current stage is terminal": true,
}

// ActionPreconditionExpression is the normalized executable grammar shared by
// Action validation and conservative consumers of formally declared
// preconditions. Operands preserve Runtime comparison semantics: membership
// values are comma-separated literals, while comparison operators expose one
// right-hand operand.
type ActionPreconditionExpression struct {
	Field    string
	Operator string
	Operands []string
}

// ActionParsePreconditionExpression parses the exact precondition grammar the
// Runtime validates. Callers must still restrict which operators they can
// safely interpret; parsing an expression does not broaden its semantics.
func ActionParsePreconditionExpression(value string) (ActionPreconditionExpression, error) {
	text := strings.TrimSpace(strings.ToLower(value))
	if text == "" {
		return ActionPreconditionExpression{}, fmt.Errorf("precondition is empty")
	}
	if actionPreconditionRuntimeConstants[text] {
		return ActionPreconditionExpression{Operator: "constant", Operands: []string{text}}, nil
	}
	if strings.HasSuffix(text, " is present") {
		field := strings.TrimSpace(strings.TrimSuffix(text, " is present"))
		if err := actionValidatePreconditionField(field); err != nil {
			return ActionPreconditionExpression{}, err
		}
		return ActionPreconditionExpression{Field: field, Operator: "is present"}, nil
	}
	for _, operator := range []string{" not in ", " in "} {
		if !strings.Contains(text, operator) {
			continue
		}
		parts := strings.SplitN(text, operator, 2)
		field := strings.TrimSpace(parts[0])
		if err := actionValidatePreconditionField(field); err != nil {
			return ActionPreconditionExpression{}, err
		}
		operands := []string{}
		for _, item := range strings.Split(parts[1], ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return ActionPreconditionExpression{}, fmt.Errorf("membership value is empty")
			}
			operands = append(operands, item)
		}
		return ActionPreconditionExpression{Field: field, Operator: strings.TrimSpace(operator), Operands: operands}, nil
	}
	for _, operator := range []string{" >= ", " <= ", " == ", " != ", " > ", " < "} {
		if !strings.Contains(text, operator) {
			continue
		}
		parts := strings.SplitN(text, operator, 2)
		field := strings.TrimSpace(parts[0])
		if err := actionValidatePreconditionField(field); err != nil {
			return ActionPreconditionExpression{}, err
		}
		right := strings.TrimSpace(parts[1])
		validatedRight := strings.Trim(right, "\"'")
		if validatedRight == "" {
			return ActionPreconditionExpression{}, fmt.Errorf("comparison value is empty")
		}
		if operator != " == " && operator != " != " && validatedRight != "today" {
			if _, err := strconv.ParseFloat(validatedRight, 64); err != nil && !actionPreconditionFieldPattern.MatchString(validatedRight) {
				return ActionPreconditionExpression{}, fmt.Errorf("comparison operand %q must be a field, number or today", validatedRight)
			}
		}
		return ActionPreconditionExpression{Field: field, Operator: strings.TrimSpace(operator), Operands: []string{right}}, nil
	}
	return ActionPreconditionExpression{}, fmt.Errorf("unsupported precondition grammar")
}

func ActionValidatePreconditionExpression(value string) error {
	_, err := ActionParsePreconditionExpression(value)
	return err
}

func actionValidatePreconditionField(value string) error {
	if !actionPreconditionFieldPattern.MatchString(value) {
		return fmt.Errorf("invalid field %q", value)
	}
	return nil
}
