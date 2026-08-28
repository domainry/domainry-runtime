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
	Offset     int
}

func (q RecordQuery) Valid() bool {
	if strings.TrimSpace(q.ObjectKey) == "" {
		return false
	}
	if q.Limit < 0 || q.Offset < 0 || (q.Offset > 0 && (q.Limit == 0 || q.Offset%q.Limit != 0)) {
		return false
	}
	switch q.Operation {
	case QueryGet, QueryGetForUpdate:
		return strings.TrimSpace(q.RecordID) != "" && len(q.Filters) == 0 && len(q.Sorts) == 0 && len(q.Projection) == 0 && q.Limit == 0 && q.Offset == 0
	case QueryList:
		return strings.TrimSpace(q.RecordID) == ""
	case QueryExists, QueryCount:
		return strings.TrimSpace(q.RecordID) == "" && len(q.Sorts) == 0 && len(q.Projection) == 0 && q.Limit == 0 && q.Offset == 0
	default:
		return false
	}
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
