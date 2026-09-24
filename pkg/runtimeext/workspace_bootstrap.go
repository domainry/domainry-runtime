package runtimeext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type WorkspaceBootstrapInputType string

const (
	WorkspaceBootstrapInputString       WorkspaceBootstrapInputType = "string"
	WorkspaceBootstrapInputInteger      WorkspaceBootstrapInputType = "integer"
	WorkspaceBootstrapInputNumber       WorkspaceBootstrapInputType = "number"
	WorkspaceBootstrapInputExactDecimal WorkspaceBootstrapInputType = "exact_decimal"
	WorkspaceBootstrapInputBoolean      WorkspaceBootstrapInputType = "boolean"
)

var workspaceBootstrapExactDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func (value WorkspaceBootstrapInputType) Valid() bool {
	switch value {
	case WorkspaceBootstrapInputString, WorkspaceBootstrapInputInteger, WorkspaceBootstrapInputNumber, WorkspaceBootstrapInputExactDecimal, WorkspaceBootstrapInputBoolean:
		return true
	default:
		return false
	}
}

type WorkspaceBootstrapInputField struct {
	Key       string
	Type      WorkspaceBootstrapInputType
	Required  bool
	Default   any
	Enum      []string
	Minimum   *float64
	Maximum   *float64
	MinLength *int
	MaxLength *int
	Pattern   string
	Format    string
}

// WorkspaceBootstrapRecordCapability freezes the exact application object and
// writable business fields for one deterministic bootstrap record. Runtime
// supplies workspace_id, id, owner_org_id, and timestamps.
type WorkspaceBootstrapRecordCapability struct {
	Key       string
	ObjectKey string
	Fields    []string
}

type WorkspaceBootstrapDescriptor struct {
	Key                 string
	InputType           string
	InputContractSHA256 string
	ParticipantRevision string
	InputFields         []WorkspaceBootstrapInputField
	Records             []WorkspaceBootstrapRecordCapability
}

func (descriptor WorkspaceBootstrapDescriptor) Validate() error {
	if strings.TrimSpace(descriptor.Key) == "" || !handlerTypeIdentityPattern.MatchString(strings.TrimSpace(descriptor.InputType)) ||
		!handlerContractHashPattern.MatchString(strings.TrimSpace(descriptor.InputContractSHA256)) || strings.TrimSpace(descriptor.ParticipantRevision) == "" {
		return ErrWorkspaceBootstrapContractInvalid
	}
	inputs := map[string]bool{}
	for _, field := range descriptor.InputFields {
		key := strings.TrimSpace(field.Key)
		if !handlerFieldIdentityPattern.MatchString(key) || !field.Type.Valid() || inputs[key] || validateWorkspaceBootstrapInputField(field) != nil {
			return ErrWorkspaceBootstrapContractInvalid
		}
		inputs[key] = true
	}
	records := map[string]bool{}
	for _, record := range descriptor.Records {
		key, objectKey := strings.TrimSpace(record.Key), strings.TrimSpace(record.ObjectKey)
		if !handlerFieldIdentityPattern.MatchString(key) || !handlerFieldIdentityPattern.MatchString(objectKey) || records[key] || len(record.Fields) == 0 {
			return ErrWorkspaceBootstrapContractInvalid
		}
		records[key] = true
		fields := map[string]bool{}
		for _, raw := range record.Fields {
			field := strings.TrimSpace(raw)
			if !handlerFieldIdentityPattern.MatchString(field) || fields[field] || field == "workspace_id" || field == "id" || field == "owner_org_id" || field == "created_at" || field == "updated_at" {
				return ErrWorkspaceBootstrapContractInvalid
			}
			fields[field] = true
		}
	}
	if len(descriptor.Records) == 0 {
		return ErrWorkspaceBootstrapContractInvalid
	}
	if descriptor.InputContractSHA256 != descriptor.ComputedInputContractSHA256() {
		return ErrWorkspaceBootstrapContractInvalid
	}
	return nil
}

// ComputedInputContractSHA256 binds the participant's declared input identity
// to the exact controlled field schema. Fields and enum values are sorted so
// source order cannot create a different authority.
func (descriptor WorkspaceBootstrapDescriptor) ComputedInputContractSHA256() string {
	type canonicalField struct {
		Key       string                      `json:"key"`
		Type      WorkspaceBootstrapInputType `json:"type"`
		Required  bool                        `json:"required"`
		Default   any                         `json:"default,omitempty"`
		Enum      []string                    `json:"enum,omitempty"`
		Minimum   *float64                    `json:"minimum,omitempty"`
		Maximum   *float64                    `json:"maximum,omitempty"`
		MinLength *int                        `json:"min_length,omitempty"`
		MaxLength *int                        `json:"max_length,omitempty"`
		Pattern   string                      `json:"pattern,omitempty"`
		Format    string                      `json:"format,omitempty"`
	}
	fields := make([]canonicalField, 0, len(descriptor.InputFields))
	for _, field := range descriptor.InputFields {
		enums := append([]string(nil), field.Enum...)
		sort.Strings(enums)
		fields = append(fields, canonicalField{
			Key: strings.TrimSpace(field.Key), Type: field.Type, Required: field.Required, Default: field.Default,
			Enum: enums, Minimum: field.Minimum, Maximum: field.Maximum, MinLength: field.MinLength, MaxLength: field.MaxLength,
			Pattern: strings.TrimSpace(field.Pattern), Format: strings.TrimSpace(field.Format),
		})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	payload, _ := json.Marshal(struct {
		Key       string           `json:"key"`
		InputType string           `json:"input_type"`
		Fields    []canonicalField `json:"fields"`
	}{Key: strings.TrimSpace(descriptor.Key), InputType: strings.TrimSpace(descriptor.InputType), Fields: fields})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func validateWorkspaceBootstrapInputField(field WorkspaceBootstrapInputField) error {
	stringConstraints := len(field.Enum) != 0 || field.MinLength != nil || field.MaxLength != nil || strings.TrimSpace(field.Pattern) != "" || strings.TrimSpace(field.Format) != ""
	numberConstraints := field.Minimum != nil || field.Maximum != nil
	if stringConstraints && field.Type != WorkspaceBootstrapInputString || numberConstraints && field.Type != WorkspaceBootstrapInputInteger && field.Type != WorkspaceBootstrapInputNumber && field.Type != WorkspaceBootstrapInputExactDecimal {
		return ErrWorkspaceBootstrapContractInvalid
	}
	if field.Minimum != nil && (math.IsNaN(*field.Minimum) || math.IsInf(*field.Minimum, 0)) || field.Maximum != nil && (math.IsNaN(*field.Maximum) || math.IsInf(*field.Maximum, 0)) || field.Minimum != nil && field.Maximum != nil && *field.Minimum > *field.Maximum {
		return ErrWorkspaceBootstrapContractInvalid
	}
	if field.Type == WorkspaceBootstrapInputInteger && (field.Minimum != nil && !workspaceBootstrapSafeConstraintInteger(*field.Minimum) || field.Maximum != nil && !workspaceBootstrapSafeConstraintInteger(*field.Maximum)) {
		return ErrWorkspaceBootstrapContractInvalid
	}
	if field.MinLength != nil && *field.MinLength < 0 || field.MaxLength != nil && *field.MaxLength < 0 || field.MinLength != nil && field.MaxLength != nil && *field.MinLength > *field.MaxLength {
		return ErrWorkspaceBootstrapContractInvalid
	}
	seenEnums := map[string]bool{}
	for _, value := range field.Enum {
		if seenEnums[value] {
			return ErrWorkspaceBootstrapContractInvalid
		}
		seenEnums[value] = true
	}
	if pattern := strings.TrimSpace(field.Pattern); pattern != "" {
		if _, err := regexp.Compile(pattern); err != nil {
			return ErrWorkspaceBootstrapContractInvalid
		}
	}
	switch strings.TrimSpace(field.Format) {
	case "", "date", "time", "date-time", "email", "uri":
	default:
		return ErrWorkspaceBootstrapContractInvalid
	}
	if field.Default != nil {
		if _, err := NormalizeWorkspaceBootstrapInputValue(field, field.Default); err != nil {
			return ErrWorkspaceBootstrapContractInvalid
		}
	}
	return nil
}

// NormalizeWorkspaceBootstrapInputValue applies the same controlled type and
// constraint contract used by descriptor validation and Runtime execution.
func NormalizeWorkspaceBootstrapInputValue(field WorkspaceBootstrapInputField, value any) (any, error) {
	invalid := func() (any, error) { return nil, fmt.Errorf("Workspace bootstrap input %q is invalid", field.Key) }
	switch field.Type {
	case WorkspaceBootstrapInputString:
		current, ok := value.(string)
		if !ok {
			return invalid()
		}
		length := utf8.RuneCountInString(current)
		if field.MinLength != nil && length < *field.MinLength || field.MaxLength != nil && length > *field.MaxLength {
			return invalid()
		}
		if len(field.Enum) != 0 {
			found := false
			for _, candidate := range field.Enum {
				found = found || current == candidate
			}
			if !found {
				return invalid()
			}
		}
		if pattern := strings.TrimSpace(field.Pattern); pattern != "" {
			compiled, err := regexp.Compile(pattern)
			if err != nil || !compiled.MatchString(current) {
				return invalid()
			}
		}
		if !workspaceBootstrapStringFormatValid(strings.TrimSpace(field.Format), current) {
			return invalid()
		}
		return current, nil
	case WorkspaceBootstrapInputBoolean:
		current, ok := value.(bool)
		if !ok {
			return invalid()
		}
		return current, nil
	case WorkspaceBootstrapInputInteger:
		current, ok := workspaceBootstrapInteger(value)
		if !ok || field.Minimum != nil && current < int64(*field.Minimum) || field.Maximum != nil && current > int64(*field.Maximum) {
			return invalid()
		}
		return current, nil
	case WorkspaceBootstrapInputNumber:
		current, ok := workspaceBootstrapNumber(value)
		if !ok || !workspaceBootstrapNumberRangeValid(field, current) {
			return invalid()
		}
		return current, nil
	case WorkspaceBootstrapInputExactDecimal:
		current, ok := value.(string)
		if !ok || !workspaceBootstrapExactDecimalPattern.MatchString(current) || !workspaceBootstrapExactDecimalRangeValid(field, current) {
			return invalid()
		}
		return current, nil
	default:
		return invalid()
	}
}

func workspaceBootstrapInteger(value any) (int64, bool) {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Int64()
		return parsed, err == nil
	}
	current := reflect.ValueOf(value)
	if !current.IsValid() {
		return 0, false
	}
	switch current.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return current.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed := current.Uint()
		if parsed > math.MaxInt64 {
			return 0, false
		}
		return int64(parsed), true
	case reflect.Float32, reflect.Float64:
		parsed := current.Float()
		const maximumSafeInteger = float64(1<<53 - 1)
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) || math.Trunc(parsed) != parsed || parsed < -maximumSafeInteger || parsed > maximumSafeInteger {
			return 0, false
		}
		integer := int64(parsed)
		return integer, float64(integer) == parsed
	default:
		return 0, false
	}
}

func workspaceBootstrapSafeConstraintInteger(value float64) bool {
	const maximumSafeInteger = float64(1<<53 - 1)
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value >= -maximumSafeInteger && value <= maximumSafeInteger
}

func workspaceBootstrapNumber(value any) (float64, bool) {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	}
	current := reflect.ValueOf(value)
	if !current.IsValid() {
		return 0, false
	}
	switch current.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(current.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(current.Uint()), true
	case reflect.Float32, reflect.Float64:
		parsed := current.Float()
		return parsed, !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	default:
		return 0, false
	}
}

func workspaceBootstrapNumberRangeValid(field WorkspaceBootstrapInputField, value float64) bool {
	return (field.Minimum == nil || value >= *field.Minimum) && (field.Maximum == nil || value <= *field.Maximum)
}

func workspaceBootstrapExactDecimalRangeValid(field WorkspaceBootstrapInputField, value string) bool {
	current, ok := new(big.Rat).SetString(value)
	if !ok {
		return false
	}
	constraint := func(value float64) *big.Rat {
		parsed, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
		return parsed
	}
	return (field.Minimum == nil || current.Cmp(constraint(*field.Minimum)) >= 0) &&
		(field.Maximum == nil || current.Cmp(constraint(*field.Maximum)) <= 0)
}

func workspaceBootstrapStringFormatValid(format, value string) bool {
	switch format {
	case "":
		return true
	case "date":
		_, err := time.Parse("2006-01-02", value)
		return err == nil
	case "time":
		_, err := time.Parse("15:04:05Z07:00", value)
		return err == nil
	case "date-time":
		_, err := time.Parse(time.RFC3339, value)
		return err == nil
	case "email":
		address, err := mail.ParseAddress(value)
		return err == nil && address.Address == value
	case "uri":
		parsed, err := url.ParseRequestURI(value)
		return err == nil && parsed.IsAbs()
	default:
		return false
	}
}

// WorkspaceBootstrapContext contains only Runtime-injected references. The
// physical Workspace database ID remains private; generated code receives the
// canonical Workspace code plus Identity-owned organization/user IDs.
type WorkspaceBootstrapContext struct {
	WorkspaceCode              string
	FirstStoreOrganizationID   string
	InitialAdministratorUserID string
}

// WorkspaceBootstrapRecord selects only a compile-time capability key. The
// participant cannot select a database object or ownership field.
type WorkspaceBootstrapRecord struct {
	CapabilityKey string
	Data          map[string]any
}

type WorkspaceBootstrapParticipant interface {
	Descriptor() WorkspaceBootstrapDescriptor
	BuildWorkspaceBootstrap(context.Context, WorkspaceBootstrapContext, map[string]any) ([]WorkspaceBootstrapRecord, error)
}

var (
	ErrWorkspaceBootstrapParticipantRequired  = errors.New("workspace bootstrap participant is required")
	ErrWorkspaceBootstrapParticipantDuplicate = errors.New("workspace bootstrap participant is already registered")
	ErrWorkspaceBootstrapContractInvalid      = errors.New("workspace bootstrap participant contract is invalid")
)
