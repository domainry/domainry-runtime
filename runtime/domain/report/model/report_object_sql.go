package reportmodel

// ReportObjectSQLSchema opts one Report into the restricted object_sql_v1
// execution path. SQL authors can reference only published object and field
// keys. Physical storage, workspace predicates, and Runtime RLS are injected
// after parsing and semantic binding.
type ReportObjectSQLSchema struct {
	SQL                 string                       `json:"sql"`
	SourceObjects       []string                     `json:"source_objects"`
	Parameters          []ReportObjectSQLParameter   `json:"parameters,omitempty"`
	ResultSchema        []ReportResultColumnSchema   `json:"result_schema"`
	JoinCardinalities   []ReportObjectSQLCardinality `json:"join_cardinalities,omitempty"`
	TimeoutMilliseconds int                          `json:"timeout_milliseconds,omitempty"`
}

func ReportObjectSQLObjectKeys(schema *ReportObjectSQLSchema) []string {
	if schema == nil {
		return nil
	}
	return append([]string(nil), schema.SourceObjects...)
}

type ReportObjectSQLParameter struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
}

type ReportResultColumnSchema struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	Kind      string `json:"kind"`
	Precision int    `json:"precision,omitempty"`
	Scale     int    `json:"scale,omitempty"`
}

type ReportObjectSQLCardinality struct {
	Alias       string `json:"alias"`
	Cardinality string `json:"cardinality"`
}

// ReportObjectSQLPlan is an internal, publication-validated lowering of a
// third-party parser AST. It is not an authoring AST and is never decoded from
// JSON. Every source and field has already been bound to published metadata.
type ReportObjectSQLPlan struct {
	Sources      []ReportObjectSQLSource
	Projections  []ReportObjectSQLProjection
	Where        *ReportObjectSQLExpression
	GroupBy      []ReportObjectSQLExpression
	Having       *ReportObjectSQLExpression
	OrderBy      []ReportObjectSQLOrder
	Limit        int
	Parameters   map[string]ReportObjectSQLParameter
	ResultSchema []ReportResultColumnSchema
	NodeCount    int
	// ExplicitOrderBy and ExplicitLimit record whether the authored SQL text
	// declared its own ORDER BY clause and literal LIMIT. Execution never
	// depends on them (the compiler appends stable tie terms and applies the
	// default limit either way); validation uses them to reject authored SQL
	// that would silently rely on the implicit 1000-row default.
	ExplicitOrderBy bool
	ExplicitLimit   bool
}

type ReportObjectSQLSource struct {
	ObjectKey   string
	Alias       string
	JoinType    string
	Cardinality string
	On          *ReportObjectSQLExpression
	Fields      []string
}

type ReportObjectSQLProjection struct {
	Alias      string
	Expression ReportObjectSQLExpression
}

type ReportObjectSQLOrder struct {
	Expression ReportObjectSQLExpression
	Direction  string
}

type ReportObjectSQLExpression struct {
	Kind      string
	Operator  string
	Name      string
	Alias     string
	FieldKey  string
	Value     string
	ValueType string
	Type      string
	Precision int
	Scale     int
	Distinct  bool
	Arguments []ReportObjectSQLExpression
	Whens     []ReportObjectSQLWhen
	Else      *ReportObjectSQLExpression
}

type ReportObjectSQLWhen struct {
	Condition ReportObjectSQLExpression
	Value     ReportObjectSQLExpression
}

type ReportObjectSQLPlanError struct {
	Code   string
	Path   string
	Params map[string]string
}

func (e *ReportObjectSQLPlanError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + " at " + e.Path
}

func (e *ReportObjectSQLPlanError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}
