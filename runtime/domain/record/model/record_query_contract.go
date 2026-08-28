package recordmodel

const (
	RecordQueryLockNone                = "none"
	RecordQueryLockForUpdate           = "for_update"
	RecordQueryLockForUpdateSkipLocked = "for_update_skip_locked"
)

// RecordFilterExpression is the canonical, persistence-neutral query filter
// tree. Logical nodes own Children; comparison nodes own Field and either
// Value or Values. Runtime query owners must normalize this tree against the
// published object schema before it reaches a SQL builder.
type RecordFilterExpression struct {
	Operator string                   `json:"operator"`
	Field    string                   `json:"field,omitempty"`
	Value    any                      `json:"value,omitempty"`
	Values   []any                    `json:"values,omitempty"`
	Children []RecordFilterExpression `json:"children,omitempty"`
}
