package runtimeext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

var (
	ErrAssigneeResolverContractInvalid = errors.New("workflow assignee resolver contract is invalid")
	ErrAssigneeResolverDuplicate       = errors.New("workflow assignee resolver is already registered")
	ErrAssigneeResolverConfigInvalid   = errors.New("workflow assignee resolver config is invalid")
	ErrAssigneeResolverGrantDenied     = errors.New("workflow assignee resolver capability is denied")
	ErrAssigneeResolverBudgetExceeded  = errors.New("workflow assignee resolver budget is exceeded")
	ErrAssigneeResolverResultInvalid   = errors.New("workflow assignee resolver result is invalid")
)

type AssigneeResolverConfigType string

const (
	AssigneeResolverConfigString     AssigneeResolverConfigType = "string"
	AssigneeResolverConfigInteger    AssigneeResolverConfigType = "integer"
	AssigneeResolverConfigNumber     AssigneeResolverConfigType = "number"
	AssigneeResolverConfigBoolean    AssigneeResolverConfigType = "boolean"
	AssigneeResolverConfigStringList AssigneeResolverConfigType = "string_list"
)

func (value AssigneeResolverConfigType) Valid() bool {
	switch value {
	case AssigneeResolverConfigString, AssigneeResolverConfigInteger, AssigneeResolverConfigNumber, AssigneeResolverConfigBoolean, AssigneeResolverConfigStringList:
		return true
	default:
		return false
	}
}

type AssigneeResolverConfigField struct {
	Key      string
	Type     AssigneeResolverConfigType
	Required bool
	Enum     []string
}

// AssigneeResolverRecordCapability is one named, read-only record projection.
// Runtime returns only Fields and accepts filters only from FilterFields.
type AssigneeResolverRecordCapability struct {
	Key          string
	ObjectKey    string
	Fields       []string
	FilterFields []string
	MaxRows      int
}

// AssigneeResolverRelationCapability is one bounded relation traversal. The
// source relation field supplies target record IDs; Runtime reads and projects
// at most MaxTargets target records through the same authorized Record Reader.
type AssigneeResolverRelationCapability struct {
	Key              string
	SourceObjectKey  string
	RelationFieldKey string
	TargetObjectKey  string
	TargetFields     []string
	MaxTargets       int
}

const (
	AssigneeIdentityProjectionFindUser     = "find_user"
	AssigneeIdentityProjectionUsersForRole = "users_for_role"
)

type AssigneeResolverDescriptor struct {
	ResolverKey          string
	ResolverRevision     string
	ConfigContractSHA256 string
	ConfigFields         []AssigneeResolverConfigField
	RecordCapabilities   []AssigneeResolverRecordCapability
	RelationCapabilities []AssigneeResolverRelationCapability
	IdentityProjections  []string
	CandidateRoleKeys    []string
	MaxReadOperations    int
	MaxCandidates        int
	TimeoutMilliseconds  int
}

func (descriptor AssigneeResolverDescriptor) Validate() error {
	if !stableAssigneeResolverKey(strings.TrimSpace(descriptor.ResolverKey)) || strings.TrimSpace(descriptor.ResolverRevision) == "" ||
		!handlerContractHashPattern.MatchString(strings.TrimSpace(descriptor.ConfigContractSHA256)) ||
		descriptor.MaxReadOperations < 1 || descriptor.MaxReadOperations > 100 || descriptor.MaxCandidates < 1 || descriptor.MaxCandidates > 500 ||
		descriptor.TimeoutMilliseconds < 1 || descriptor.TimeoutMilliseconds > 5000 {
		return ErrAssigneeResolverContractInvalid
	}
	fields := map[string]bool{}
	for _, field := range descriptor.ConfigFields {
		key := strings.TrimSpace(field.Key)
		if !handlerFieldIdentityPattern.MatchString(key) || !field.Type.Valid() || fields[key] || field.Type != AssigneeResolverConfigString && len(field.Enum) != 0 || hasDuplicateOrBlank(field.Enum) {
			return ErrAssigneeResolverContractInvalid
		}
		fields[key] = true
	}
	records := map[string]bool{}
	for _, capability := range descriptor.RecordCapabilities {
		key := strings.TrimSpace(capability.Key)
		if !handlerFieldIdentityPattern.MatchString(key) || !stableAssigneeResolverKey(strings.TrimSpace(capability.ObjectKey)) || records[key] || len(capability.Fields) == 0 || capability.MaxRows < 1 || capability.MaxRows > 500 || !validAssigneeFieldSet(capability.Fields) || !validAssigneeFieldSet(capability.FilterFields) {
			return ErrAssigneeResolverContractInvalid
		}
		records[key] = true
	}
	relations := map[string]bool{}
	for _, capability := range descriptor.RelationCapabilities {
		key := strings.TrimSpace(capability.Key)
		if !handlerFieldIdentityPattern.MatchString(key) || relations[key] || !stableAssigneeResolverKey(strings.TrimSpace(capability.SourceObjectKey)) || !handlerFieldIdentityPattern.MatchString(strings.TrimSpace(capability.RelationFieldKey)) || !stableAssigneeResolverKey(strings.TrimSpace(capability.TargetObjectKey)) || len(capability.TargetFields) == 0 || !validAssigneeFieldSet(capability.TargetFields) || capability.MaxTargets < 1 || capability.MaxTargets > 500 {
			return ErrAssigneeResolverContractInvalid
		}
		relations[key] = true
	}
	identity := map[string]bool{}
	for _, raw := range descriptor.IdentityProjections {
		projection := strings.TrimSpace(raw)
		if projection != AssigneeIdentityProjectionFindUser && projection != AssigneeIdentityProjectionUsersForRole || identity[projection] {
			return ErrAssigneeResolverContractInvalid
		}
		identity[projection] = true
	}
	if hasDuplicateOrBlank(descriptor.CandidateRoleKeys) {
		return ErrAssigneeResolverContractInvalid
	}
	for _, roleKey := range descriptor.CandidateRoleKeys {
		if !stableAssigneeResolverKey(strings.TrimSpace(roleKey)) {
			return ErrAssigneeResolverContractInvalid
		}
	}
	if descriptor.ConfigContractSHA256 != descriptor.ComputedConfigContractSHA256() {
		return ErrAssigneeResolverContractInvalid
	}
	return nil
}

func (descriptor AssigneeResolverDescriptor) ComputedConfigContractSHA256() string {
	fields := append([]AssigneeResolverConfigField(nil), descriptor.ConfigFields...)
	for index := range fields {
		fields[index].Key = strings.TrimSpace(fields[index].Key)
		fields[index].Enum = append([]string(nil), fields[index].Enum...)
		sort.Strings(fields[index].Enum)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	payload, _ := json.Marshal(struct {
		ResolverKey string
		Fields      []AssigneeResolverConfigField
	}{ResolverKey: strings.TrimSpace(descriptor.ResolverKey), Fields: fields})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

type AssigneeRecordFilter struct {
	Field    string
	Operator string
	Values   []string
}

type AssigneeRecordListRequest struct {
	CapabilityKey string
	Filters       []AssigneeRecordFilter
	Limit         int
}

type AssigneeRelationRequest struct {
	CapabilityKey  string
	SourceRecordID string
}

type AssigneeRecord struct {
	ObjectKey string
	RecordID  string
	Fields    map[string]any
}

type AssigneeIdentityUser struct {
	UserID        string
	DisplayName   string
	ManagerUserID string
	Status        string
}

type AssigneeResolverCapabilities interface {
	GetRecord(context.Context, string, string) (AssigneeRecord, bool, error)
	ListRecords(context.Context, AssigneeRecordListRequest) ([]AssigneeRecord, error)
	FollowRelation(context.Context, AssigneeRelationRequest) ([]AssigneeRecord, error)
	FindUser(context.Context, string) (AssigneeIdentityUser, bool, error)
	UsersForRole(context.Context, string) ([]AssigneeIdentityUser, error)
}

type AssigneeResolverContext struct {
	WorkspaceID       string
	ProcessID         string
	WorkflowKey       string
	DefinitionVersion int
	NodeID            string
	ObjectKey         string
	RecordID          string
	InitiatorUserID   string
	InitiatorRoleKey  string
	Variables         map[string]any
	Config            map[string]any
}

type AssigneeEvidenceFact struct {
	Key   string
	Value string
}

type AssigneeResolverCandidate struct {
	UserID   string
	RoleKey  string
	Evidence []AssigneeEvidenceFact
}

type AssigneeResolver interface {
	Descriptor() AssigneeResolverDescriptor
	Resolve(context.Context, AssigneeResolverCapabilities, AssigneeResolverContext) ([]AssigneeResolverCandidate, error)
}

type AssigneeResolverBinding struct {
	Descriptor AssigneeResolverDescriptor
	Resolver   AssigneeResolver
}

func NormalizeAssigneeResolverConfig(descriptor AssigneeResolverDescriptor, config map[string]any) (map[string]any, error) {
	declared := make(map[string]AssigneeResolverConfigField, len(descriptor.ConfigFields))
	for _, field := range descriptor.ConfigFields {
		declared[strings.TrimSpace(field.Key)] = field
	}
	result := make(map[string]any, len(config))
	for key, value := range config {
		normalizedKey := strings.TrimSpace(key)
		field, exists := declared[normalizedKey]
		if !exists {
			return nil, fmt.Errorf("%w: unknown field %s", ErrAssigneeResolverConfigInvalid, key)
		}
		if _, duplicate := result[normalizedKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate normalized field %s", ErrAssigneeResolverConfigInvalid, normalizedKey)
		}
		normalized, ok := normalizeAssigneeResolverConfigValue(field, value)
		if !ok {
			return nil, fmt.Errorf("%w: field %s", ErrAssigneeResolverConfigInvalid, key)
		}
		result[normalizedKey] = normalized
	}
	for key, field := range declared {
		if field.Required {
			if _, exists := result[key]; !exists {
				return nil, fmt.Errorf("%w: required field %s", ErrAssigneeResolverConfigInvalid, key)
			}
		}
	}
	return result, nil
}

func normalizeAssigneeResolverConfigValue(field AssigneeResolverConfigField, value any) (any, bool) {
	switch field.Type {
	case AssigneeResolverConfigString:
		current, ok := value.(string)
		if !ok {
			return nil, false
		}
		if len(field.Enum) != 0 {
			for _, candidate := range field.Enum {
				if current == candidate {
					return current, true
				}
			}
			return nil, false
		}
		return current, true
	case AssigneeResolverConfigBoolean:
		current, ok := value.(bool)
		return current, ok
	case AssigneeResolverConfigStringList:
		switch current := value.(type) {
		case []string:
			return append([]string(nil), current...), true
		case []any:
			result := make([]string, len(current))
			for index, item := range current {
				text, ok := item.(string)
				if !ok {
					return nil, false
				}
				result[index] = text
			}
			return result, true
		default:
			return nil, false
		}
	case AssigneeResolverConfigInteger:
		current, ok := workspaceBootstrapInteger(value)
		return current, ok
	case AssigneeResolverConfigNumber:
		if current, ok := value.(json.Number); ok {
			parsed, err := current.Float64()
			return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
		}
		current := reflect.ValueOf(value)
		if !current.IsValid() {
			return nil, false
		}
		switch current.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return float64(current.Int()), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return float64(current.Uint()), true
		case reflect.Float32, reflect.Float64:
			parsed := current.Float()
			return parsed, !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func stableAssigneeResolverKey(value string) bool {
	if value == "" {
		return false
	}
	for index, current := range value {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' && index > 0 || index > 0 && (current == '_' || current == '-' || current == '.') {
			continue
		}
		return false
	}
	return true
}

func validAssigneeFieldSet(values []string) bool {
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if !handlerFieldIdentityPattern.MatchString(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func hasDuplicateOrBlank(values []string) bool {
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}
