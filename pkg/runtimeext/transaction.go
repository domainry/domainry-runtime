package runtimeext

import "strings"

// Record is the generated-binding representation of one Runtime record. Raw
// keys and values stop at generated code and are not exposed by generated
// Action capabilities.
type Record struct {
	ID        string
	ObjectKey string
	Fields    map[string]any
	Version   int64
	UpdatedAt string
}

type QueryOperation string

const (
	QueryGet          QueryOperation = "get"
	QueryGetForUpdate QueryOperation = "get_for_update"
	QueryList         QueryOperation = "list"
	QueryExists       QueryOperation = "exists"
	QueryCount        QueryOperation = "count"
)

type Filter struct {
	Field    string
	Operator string
	Value    any
	Values   []any
	Children []Filter
}

type Sort struct {
	Field     string
	Direction string
}

type RecordQuery struct {
	Operation  QueryOperation
	ObjectKey  string
	RecordID   string
	Filters    []Filter
	Sorts      []Sort
	Projection []string
	Limit      int
	AfterID    string
	// StoreOrganization is an execution-scoped catalog reference used only by
	// generated catalog-owned list methods. It is not a business-field filter.
	StoreOrganization *StoreOrganizationCatalogItem
}

func (q RecordQuery) Valid() bool {
	if strings.TrimSpace(q.ObjectKey) == "" {
		return false
	}
	if q.Limit < 0 {
		return false
	}
	switch q.Operation {
	case QueryGet, QueryGetForUpdate:
		return q.StoreOrganization == nil && strings.TrimSpace(q.RecordID) != "" && len(q.Filters) == 0 && len(q.Sorts) == 0 && len(q.Projection) == 0 && q.Limit == 0 && strings.TrimSpace(q.AfterID) == ""
	case QueryList:
		if strings.TrimSpace(q.RecordID) != "" || recordQueryContainsRuntimeOwnedFilter(q.Filters) {
			return false
		}
		if q.StoreOrganization != nil {
			_, _, valid := ResolveStoreOrganizationCatalogItem(*q.StoreOrganization)
			return valid
		}
		return true
	case QueryExists, QueryCount:
		return q.StoreOrganization == nil && !recordQueryContainsRuntimeOwnedFilter(q.Filters) && strings.TrimSpace(q.RecordID) == "" && len(q.Sorts) == 0 && len(q.Projection) == 0 && q.Limit == 0 && strings.TrimSpace(q.AfterID) == ""
	default:
		return false
	}
}

func recordQueryContainsRuntimeOwnedFilter(filters []Filter) bool {
	for _, filter := range filters {
		switch strings.ToLower(strings.TrimSpace(filter.Field)) {
		case "workspace_id", "owner_org_id", "owner_user_id":
			return true
		}
		if recordQueryContainsRuntimeOwnedFilter(filter.Children) {
			return true
		}
	}
	return false
}

type RecordQueryResult struct {
	Records []Record
	Exists  bool
	Count   int64
}

type MutationOperation string

const (
	MutationCreate            MutationOperation = "create"
	MutationUpdate            MutationOperation = "update"
	MutationDelete            MutationOperation = "delete"
	MutationRestore           MutationOperation = "restore"
	MutationConditionalUpdate MutationOperation = "conditional_update"
)

type Predicate struct {
	Field     string
	Operator  string
	Value     any
	ErrorCode string
}

type Arithmetic struct {
	Field     string
	Operation string
	Operand   any
}

type RecordMutation struct {
	Operation         MutationOperation
	ObjectKey         string
	RecordID          string
	Fields            map[string]any
	Predicates        []Predicate
	Arithmetic        []Arithmetic
	ExpectedVersion   *int64
	ExpectedUpdatedAt string
}

func (m RecordMutation) Valid() bool {
	if strings.TrimSpace(m.ObjectKey) == "" {
		return false
	}
	hasRecordID := strings.TrimSpace(m.RecordID) != ""
	hasConditionalInput := len(m.Predicates) != 0 || len(m.Arithmetic) != 0 || m.ExpectedVersion != nil || strings.TrimSpace(m.ExpectedUpdatedAt) != ""
	for _, predicate := range m.Predicates {
		if strings.TrimSpace(predicate.Field) == "" || strings.TrimSpace(predicate.Operator) == "" {
			return false
		}
	}
	for _, arithmetic := range m.Arithmetic {
		if strings.TrimSpace(arithmetic.Field) == "" || strings.TrimSpace(arithmetic.Operation) == "" || arithmetic.Operand == nil {
			return false
		}
	}
	for key := range m.Fields {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "workspace_id", "owner_org_id", "owner_user_id", "created_at", "updated_at":
			return false
		}
	}
	switch m.Operation {
	case MutationCreate:
		return !hasRecordID && !hasConditionalInput
	case MutationUpdate:
		return hasRecordID && !hasConditionalInput
	case MutationConditionalUpdate:
		return hasRecordID && hasConditionalInput
	case MutationDelete, MutationRestore:
		return hasRecordID && len(m.Fields) == 0 && len(m.Predicates) == 0 && len(m.Arithmetic) == 0 && m.ExpectedVersion == nil
	default:
		return false
	}
}

type RecordMutationResult struct {
	Record  Record
	Applied bool
}

const (
	ConditionalUpdateManyMinSize = 1
	ConditionalUpdateManyMaxSize = 200
)

// ConditionalUpdateManyExactCoverage declares the business identity whose
// values must be covered exactly once by the locked record set. It is explicit
// authority, not a predicate inferred from Filters: every ExpectedValue must
// identify one and only one selected record, and selected records may not
// introduce another value.
type ConditionalUpdateManyExactCoverage struct {
	Field          string
	ExpectedValues []any
}

// ConditionalUpdateManyRequest is one closed set mutation. Runtime selects and
// locks the matching rows once, validates ExpectedCount and ExactCoverage, and
// commits one conditional UPDATE in the same Action transaction. Project code
// cannot supply storage ownership or SQL.
type ConditionalUpdateManyRequest struct {
	ObjectKey     string
	Filters       []Filter
	Fields        map[string]any
	ExpectedCount int
	ExactCoverage ConditionalUpdateManyExactCoverage
}

func (request ConditionalUpdateManyRequest) Valid() bool {
	if strings.TrimSpace(request.ObjectKey) == "" || len(request.Filters) == 0 || len(request.Fields) == 0 ||
		request.ExpectedCount < ConditionalUpdateManyMinSize || request.ExpectedCount > ConditionalUpdateManyMaxSize ||
		recordQueryContainsRuntimeOwnedFilter(request.Filters) ||
		strings.TrimSpace(request.ExactCoverage.Field) == "" ||
		len(request.ExactCoverage.ExpectedValues) != request.ExpectedCount {
		return false
	}
	for _, value := range request.ExactCoverage.ExpectedValues {
		if value == nil {
			return false
		}
	}
	for key := range request.Fields {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "", "workspace_id", "owner_org_id", "owner_user_id", "created_at", "updated_at", "create_by", "update_by", "deleted", "ext_info":
			return false
		}
	}
	return RecordQuery{Operation: QueryList, ObjectKey: request.ObjectKey, Filters: request.Filters, Limit: request.ExpectedCount}.Valid()
}

type ConditionalUpdateManyResult struct {
	RecordIDs     []string
	AffectedCount int
}
