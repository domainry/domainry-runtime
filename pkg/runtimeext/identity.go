// Package runtimeext defines the stable public boundary used by generated
// project code and source-owned business Action handlers.
package runtimeext

import "strings"

// BusinessProfileReference identifies the currently selected business identity
// record without exposing Runtime-internal profile or authorization models.
type BusinessProfileReference struct {
	BindingKey string
	ObjectKey  string
	RecordID   string
}

// Principal is the immutable caller identity visible to project business code.
// Runtime-internal role, policy and scope models are intentionally not exposed.
type Principal struct {
	UserID                string
	RoleKey               string
	DepartmentID          string
	RequestID             string
	CorrelationID         string
	CausationID           string
	AuthorizationRevision string
	Known                 bool
	ActiveBusinessProfile *BusinessProfileReference
}

// Workspace identifies the tenant boundary of one Action execution.
type Workspace struct {
	ID string
}

func (w Workspace) Valid() bool { return strings.TrimSpace(w.ID) != "" }

// ExecutionIdentity is the immutable trace and contract identity for one
// Action invocation.
type ExecutionIdentity struct {
	ExecutionID               string
	ReceiptID                 string
	ActionKey                 string
	ObjectKey                 string
	RecordID                  string
	IdempotencyKey            string
	RuntimeRevision           string
	ApplicationSchemaRevision string
	ProjectRevision           string
	HandlerRevision           string
}

func (i ExecutionIdentity) Valid() bool {
	return strings.TrimSpace(i.ExecutionID) != "" && strings.TrimSpace(i.ActionKey) != ""
}
