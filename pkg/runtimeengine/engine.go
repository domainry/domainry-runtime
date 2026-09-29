// Package runtimeengine is the public in-process application boundary for
// project-owned HTTP handlers. It exposes governed Runtime use cases, never
// repositories, SQL handles, or transport-owned generic routes.
package runtimeengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type Record struct {
	ID          string         `json:"id"`
	ObjectKey   string         `json:"object_key"`
	Fields      map[string]any `json:"fields"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	OwnerUserID string         `json:"owner_user_id,omitempty"`
	// OwnerUserName is a current, read-only Identity display projection. It is
	// never accepted as a record mutation or used for authorization.
	OwnerUserName string `json:"owner_user_name,omitempty"`
}

type Sort struct {
	Field     string
	Direction string
}

type Query struct {
	Page         int
	PageSize     int
	Search       string
	SearchFields []string
	Filters      map[string]any
	Sorts        []Sort
	SelectFields []string
	// AfterID is the continuation returned in Page.NextAfterID. The historical
	// name describes the id-only fast path; for any other stable sort Runtime
	// returns an opaque, query- and principal-bound keyset cursor here.
	AfterID string
}

type Page struct {
	Items    []Record
	Page     int
	PageSize int
	Total    int
	HasNext  bool
	// NextAfterID is an opaque continuation unless the effective order is only
	// id ascending. Callers must echo it unchanged as Query.AfterID.
	NextAfterID string
}

type ActionRequest struct {
	ActionKey      string
	ObjectKey      string
	RecordID       string
	Input          any
	IdempotencyKey string
	// PreventExecutionReclaim makes an unresolved Action receipt permanent.
	// Project handlers set it only for a user-confirmed external account write:
	// a retry may replay a completed receipt but must never take over and repeat
	// an external effect after the original process disappears.
	PreventExecutionReclaim bool
	TargetOrganizationID    string
	AssuranceToken          string
}

type ActionResult struct {
	InvocationID string
	Status       string
	Output       map[string]any
	Record       *Record
}

type ActionAssuranceChallengeRequest struct {
	ActionKey   string
	ObjectKey   string
	RecordID    string
	Payload     map[string]any
	AccessToken string
}

type ActionAssuranceVerificationRequest struct {
	ActionAssuranceChallengeRequest
	Provider string
	State    string
	Code     string
}

type ActionAssuranceChallenge struct {
	Provider          string `json:"provider"`
	State             string `json:"state"`
	Type              string `json:"type,omitempty"`
	Purpose           string `json:"purpose,omitempty"`
	Status            string `json:"status,omitempty"`
	MaskedDestination string `json:"masked_destination,omitempty"`
	RetryAt           string `json:"retry_at,omitempty"`
	ExpiresAt         string `json:"expires_at"`
}

type ActionAssuranceGrant struct {
	AssuranceToken string   `json:"assurance_token"`
	GrantID        string   `json:"grant_id"`
	Methods        []string `json:"methods"`
	ExpiresAt      string   `json:"expires_at"`
}

// Engine is safe for project-owned HTTP handlers. Ordinary endpoints call the
// record methods. A real business operation calls InvokeAction, which retains
// Runtime's existing governed BusinessHandler transaction and capability model.
type Engine interface {
	List(context.Context, string, Query) (Page, error)
	Get(context.Context, string, string) (Record, error)
	Create(context.Context, string, any) (Record, error)
	Update(context.Context, string, string, any) (Record, error)
	Delete(context.Context, string, string) error
	InvokeAction(context.Context, ActionRequest) (ActionResult, error)
}

// KnowledgeSearchEngine is the optional current-principal boundary used by
// project-owned pages that render source-backed semantic search results. It
// intentionally exposes normalized citations only: provider payloads and
// library credentials never cross into project HTTP code.
type KnowledgeSearchEngine interface {
	SearchKnowledgeLibrary(context.Context, string, string) (KnowledgeSearchResult, error)
}

type KnowledgeSearchResult struct {
	LibraryID string                    `json:"library_id"`
	Query     string                    `json:"query"`
	Citations []KnowledgeSearchCitation `json:"citations"`
}

type KnowledgeSearchCitation struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	Title      string `json:"title,omitempty"`
}

// IdempotentRecordEngine is the project HTTP boundary for externally retried
// record mutations. Interactive product handlers may keep using Engine CRUD;
// API-key handlers require this interface and a caller-owned idempotency key.
type IdempotentRecordEngine interface {
	CreateRecordIdempotent(context.Context, string, any, string) (Record, bool, error)
	UpdateRecordIdempotent(context.Context, string, string, any, string) (Record, error)
	DeleteRecordIdempotent(context.Context, string, string, string, string) (bool, error)
}

// RecordDataExchangeEngine exposes Runtime's governed record import/export
// application services to project-owned HTTP. Project handlers keep ownership
// of their public routes while Runtime remains responsible for authorization,
// row/field policy, validation, idempotency, audit and Data Exchange jobs.
type RecordDataExchangeEngine interface {
	PreviewRecordImport(context.Context, string, []byte) (RecordImportPreview, error)
	ApplyRecordImportIdempotent(context.Context, string, []byte, string) (RecordImportApplyResult, bool, error)
	EnqueueRecordImport(context.Context, string, []byte, string) (RecordBatchJob, bool, error)
	DispatchRecordExportIdempotent(context.Context, string, string, RecordExportOptions) (RecordExportDispatch, error)
	DownloadRecordExport(context.Context, string) (RecordExportArtifact, error)
}

type RecordImportRowIssue struct {
	Field    string            `json:"field,omitempty"`
	Message  string            `json:"message"`
	Code     string            `json:"code,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Severity string            `json:"severity"`
}

type RecordImportPreviewRow struct {
	Row          int                    `json:"row"`
	Data         map[string]any         `json:"data"`
	RawValues    map[string]string      `json:"raw_values,omitempty"`
	Issues       []RecordImportRowIssue `json:"issues"`
	ErrorSummary string                 `json:"error_summary,omitempty"`
	Valid        bool                   `json:"valid"`
	Duplicate    bool                   `json:"duplicate"`
}

type RecordImportPreview struct {
	ObjectKey     string                   `json:"object_key"`
	Rows          []RecordImportPreviewRow `json:"rows"`
	ErrorRows     []RecordImportPreviewRow `json:"error_rows,omitempty"`
	ValidRows     int                      `json:"valid_rows"`
	InvalidRows   int                      `json:"invalid_rows"`
	DuplicateRows int                      `json:"duplicate_rows"`
	CanApply      bool                     `json:"can_apply"`
}

type RecordImportApplyResult struct {
	ObjectKey string              `json:"object_key"`
	Created   int                 `json:"created"`
	Skipped   int                 `json:"skipped"`
	Preview   RecordImportPreview `json:"preview"`
}

type RecordBatchJob struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Kind             string `json:"kind"`
	ObjectKey        string `json:"object_key"`
	Status           string `json:"status"`
	Checkpoint       int    `json:"checkpoint"`
	Total            int    `json:"total"`
	ResultFilename   string `json:"result_filename,omitempty"`
	ResultType       string `json:"result_content_type,omitempty"`
	ResultArtifactID string `json:"result_artifact_id,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	ActorID          string `json:"actor_id"`
	RoleKey          string `json:"role_key"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type RecordExportOptions struct {
	Fields         []string `json:"fields,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	MaskingPolicy  string   `json:"masking_policy,omitempty"`
	FilterSummary  string   `json:"filter_summary,omitempty"`
	Query          Query    `json:"query,omitempty"`
	AssuranceToken string   `json:"-"`
}

type RecordExportDispatch struct {
	Delivery string         `json:"delivery"`
	Content  []byte         `json:"-"`
	Filename string         `json:"filename,omitempty"`
	Job      RecordBatchJob `json:"job,omitempty"`
	Replayed bool           `json:"replayed,omitempty"`
}

type RecordExportArtifact struct {
	ID          string
	Filename    string
	ContentType string
	SHA256      string
	Size        int64
	ExpiresAt   string
	Content     io.ReadCloser
}

// ActionAssuranceEngine is the optional project-owned HTTP boundary for
// obtaining a short-lived, payload-bound grant before a protected Action.
// The access token comes from the authenticated request header and is never
// accepted from a JSON body.
type ActionAssuranceEngine interface {
	BeginActionAssurance(context.Context, ActionAssuranceChallengeRequest) (ActionAssuranceChallenge, error)
	VerifyActionAssurance(context.Context, ActionAssuranceVerificationRequest) (ActionAssuranceGrant, error)
}

// ConnectionAccountWriteAuthorizer is an optional, current-user capability for
// project HTTP flows that prepare an external write without executing it.
// Integration rechecks ownership, active credentials, granted scopes and the
// exact provider operation; the eventual action must authorize again.
type ConnectionAccountWriteAuthorizer interface {
	AuthorizeConnectionAccountWrite(context.Context, string, string, string) (ConnectionAccountWriteAccess, error)
}

type ConnectionAccountWriteAccess struct {
	ConnectionKey string
	ProviderKey   string
}

// ConnectionAccountReader is an optional current-user boundary for project
// handlers that need one registered, read-only Provider operation. Runtime
// derives identity and request identity from the authenticated request;
// Integration rechecks account ownership, state, scopes and contract before and
// after the call. Payload is never supplied with credentials or account scope.
type ConnectionAccountReader interface {
	ReadConnectionAccount(context.Context, string, string, string, any) (ConnectionAccountReadResult, error)
}

type ConnectionAccountReadResult struct {
	ConnectionKey string
	ProviderKey   string
	Payload       json.RawMessage
}

// TaskAssigneeDirectory is a narrow, authenticated Identity projection for
// project task assignment. Callers must validate again when a task is written;
// a previously displayed option is not an authorization grant.
type TaskAssigneeDirectory interface {
	ListTaskAssignees(context.Context, TaskAssigneeQuery) (TaskAssigneePage, error)
	ValidateTaskAssignee(context.Context, string) (TaskAssignee, error)
	ResolveTaskAssigneeNames(context.Context, []string) (map[string]string, error)
}

type TaskAssignee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TaskAssigneeQuery struct {
	Search  string
	AfterID string
	Limit   int
}

type TaskAssigneePage struct {
	Items       []TaskAssignee `json:"items"`
	Total       int            `json:"total"`
	NextAfterID string         `json:"next_cursor,omitempty"`
}

// HTTPFactory receives the authenticated Runtime Engine and returns the
// project's own router. Runtime mounts it below /api/ and applies its normal
// authentication, workspace admission, capacity, recovery and CORS layers.
type HTTPFactory func(Engine) http.Handler

type ErrorKind string

const (
	ErrorBadRequest      ErrorKind = "bad_request"
	ErrorUnauthenticated ErrorKind = "unauthenticated"
	ErrorForbidden       ErrorKind = "forbidden"
	ErrorNotFound        ErrorKind = "not_found"
	ErrorConflict        ErrorKind = "conflict"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorInternal        ErrorKind = "internal"
)

type Error struct {
	Kind       ErrorKind
	Code       string
	Message    string
	Parameters map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Cause }

func HTTPStatus(err error) int {
	var typed *Error
	if !errors.As(err, &typed) {
		return http.StatusInternalServerError
	}
	switch typed.Kind {
	case ErrorBadRequest:
		return http.StatusBadRequest
	case ErrorUnauthenticated:
		return http.StatusUnauthorized
	case ErrorForbidden:
		return http.StatusForbidden
	case ErrorNotFound:
		return http.StatusNotFound
	case ErrorConflict:
		return http.StatusConflict
	case ErrorRateLimited:
		return http.StatusTooManyRequests
	case ErrorUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func ErrorCode(err error) string {
	var typed *Error
	if errors.As(err, &typed) && typed.Code != "" {
		return typed.Code
	}
	return "backend.internal"
}

func AsError(err error) (*Error, bool) {
	var typed *Error
	ok := errors.As(err, &typed)
	return typed, ok
}

func NewError(kind ErrorKind, code string, parameters map[string]string, cause error) error {
	return &Error{Kind: kind, Code: code, Parameters: cloneStrings(parameters), Cause: cause}
}

func cloneStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
