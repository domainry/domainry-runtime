package recordmodel

type Record struct {
	WorkspaceID  string                        `json:"workspace_id"`
	ID           string                        `json:"id"`
	Data         map[string]any                `json:"data"`
	CreatedAt    string                        `json:"created_at"`
	UpdatedAt    string                        `json:"updated_at"`
	Deleted      bool                          `json:"deleted"`
	ExtInfo      map[string]any                `json:"ext_info,omitempty"`
	CreateUserID string                        `json:"create_user_id,omitempty"`
	UpdateUserID string                        `json:"update_user_id,omitempty"`
	Localization *RecordLocalizationResolution `json:"localization,omitempty"`
}

// RecordLocalizedValue is one translated business-record field. Stable facts
// such as SKU, price and inventory remain on the owning Record.
type RecordLocalizedValue struct {
	RecordID  string `json:"record_id"`
	FieldKey  string `json:"field_key"`
	Locale    string `json:"locale"`
	TextValue string `json:"text_value"`
}

// RecordLocalizedValueMutation is persisted in the same transaction as its
// owning Record mutation. An empty TextValue removes that locale override.
type RecordLocalizedValueMutation struct {
	FieldKey  string `json:"field_key"`
	Locale    string `json:"locale"`
	TextValue string `json:"text_value"`
}

type RecordLocalizationResolution struct {
	RequestedLocale string            `json:"requested_locale"`
	FallbackLocale  string            `json:"fallback_locale,omitempty"`
	FieldSources    map[string]string `json:"field_sources,omitempty"`
}

type RecordSortRule struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// RecordScopeExpression is the storage-neutral authorization predicate produced
// from a role data-scope filter. Relation segments are resolved against the
// installed object graph before the query reaches persistence.
type RecordScopeExpression struct {
	Operator string                   `json:"operator"`
	FieldKey string                   `json:"field_key,omitempty"`
	Path     []RecordScopePathSegment `json:"path,omitempty"`
	Values   []string                 `json:"values,omitempty"`
	Children []RecordScopeExpression  `json:"children,omitempty"`
	// RelationExists is set only by Repository scope resolution when the
	// resolved permission ID set exceeds the published IN threshold.
	RelationExists bool `json:"-"`
}

type RecordScopePathSegment struct {
	SourceObjectKey  string `json:"source_object_key"`
	Direction        string `json:"direction"`
	RelationFieldKey string `json:"relation_field_key"`
	TargetObjectKey  string `json:"target_object_key"`
}

type RecordScopeDiagnostic struct {
	Code      string `json:"code"`
	ObjectKey string `json:"object_key"`
	Scope     string `json:"scope"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type RecordListQuery struct {
	Page                      int                     `json:"page"`
	PageSize                  int                     `json:"page_size"`
	Search                    string                  `json:"search,omitempty"`
	SearchFields              []string                `json:"search_fields,omitempty"`
	Filters                   map[string]any          `json:"filters,omitempty"`
	FilterExpression          *RecordFilterExpression `json:"filter,omitempty"`
	Sort                      []RecordSortRule        `json:"sort,omitempty"`
	SelectFields              []string                `json:"select_fields,omitempty"`
	LockIntent                string                  `json:"lock_intent,omitempty"`
	ViewKey                   string                  `json:"view,omitempty"`
	Scope                     string                  `json:"-"`
	PrincipalUserID           string                  `json:"-"`
	PrincipalWorkspaceID      string                  `json:"-"`
	PrincipalDepartmentPath   string                  `json:"-"`
	PrincipalReportingUserIDs []string                `json:"-"`
	PrincipalTeamIDs          []string                `json:"-"`
	PrincipalStoreIDs         []string                `json:"-"`
	PrincipalTerritoryIDs     []string                `json:"-"`
	PrincipalWarehouseIDs     []string                `json:"-"`
	OwnerField                string                  `json:"-"`
	DepartmentPathField       string                  `json:"-"`
	TeamField                 string                  `json:"-"`
	StoreField                string                  `json:"-"`
	TerritoryField            string                  `json:"-"`
	WarehouseField            string                  `json:"-"`
	ScopeExpression           *RecordScopeExpression  `json:"-"`
	ScopeDiagnostic           *RecordScopeDiagnostic  `json:"-"`
	RootObjectKey             string                  `json:"-"`
	Locale                    string                  `json:"-"`
	FallbackLocale            string                  `json:"-"`
	SkipTotal                 bool                    `json:"-"`
	AfterID                   string                  `json:"-"`
}

type RecordPageResult struct {
	Items    []Record `json:"items"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	Total    int      `json:"total"`
	HasNext  bool     `json:"has_next"`
}
