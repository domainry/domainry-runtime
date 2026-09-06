package runtimeext

import (
	"context"
	"errors"
	"strings"
)

// IdentityHandlerOperation is the closed set of Identity-owned operations a
// generated Action wrapper may request. Runtime injects the request bearer and
// idempotency key; project code cannot supply either value.
type IdentityHandlerOperation string

type IdentityHandlerLoginMode string

const (
	IdentityHandlerCreate  IdentityHandlerOperation = "create"
	IdentityHandlerUpdate  IdentityHandlerOperation = "update"
	IdentityHandlerDisable IdentityHandlerOperation = "disable"
	IdentityHandlerResolve IdentityHandlerOperation = "resolve"

	IdentityHandlerLoginNone     IdentityHandlerLoginMode = "none"
	IdentityHandlerLoginPassword IdentityHandlerLoginMode = "password"
)

func (value IdentityHandlerOperation) Valid() bool {
	switch value {
	case IdentityHandlerCreate, IdentityHandlerUpdate, IdentityHandlerDisable, IdentityHandlerResolve:
		return true
	default:
		return false
	}
}

// IdentityProfileBindingCapability names only trusted metadata. The relation
// field is resolved by Runtime from the published profile-extension contract;
// a generated handler never names a database column.
type IdentityProfileBindingCapability struct {
	BindingKey string
	ObjectKey  string
}

// IdentityHandlerDeliveryCapability is frozen with one Handler descriptor.
// InitialCredentialOutputField is a generated response-contract field. Runtime
// alone fills it, and only after the outer Action transaction commits.
type IdentityHandlerDeliveryCapability struct {
	Operations                   []IdentityHandlerOperation
	ProfileBindings              []IdentityProfileBindingCapability
	InitialCredentialOutputField string
}

func (value IdentityHandlerDeliveryCapability) Valid() bool {
	if len(value.Operations) == 0 {
		return false
	}
	operations := map[IdentityHandlerOperation]bool{}
	for _, operation := range value.Operations {
		if !operation.Valid() || operations[operation] {
			return false
		}
		operations[operation] = true
	}
	bindings := map[string]bool{}
	for _, binding := range value.ProfileBindings {
		key, objectKey := strings.TrimSpace(binding.BindingKey), strings.TrimSpace(binding.ObjectKey)
		identity := key + "\x00" + objectKey
		if key == "" || objectKey == "" || bindings[identity] {
			return false
		}
		bindings[identity] = true
	}
	if (operations[IdentityHandlerUpdate] || operations[IdentityHandlerDisable]) && len(bindings) == 0 {
		return false
	}
	credentialField := strings.TrimSpace(value.InitialCredentialOutputField)
	return credentialField == "" || operations[IdentityHandlerCreate] && handlerFieldIdentityPattern.MatchString(credentialField)
}

type IdentityUser struct {
	ID            string
	Name          string
	GivenName     string
	MiddleName    string
	FamilyName    string
	NamePrefix    string
	NameSuffix    string
	NativeName    string
	NameLocale    string
	Email         string
	Phone         string
	AccountType   string
	Locale        string
	Timezone      string
	OrgID         string
	SupportOrgID  string
	ManagerUserID string
	ReportingPath string
	WorkerNo      string
	WorkerType    string
	WorkStatus    string
	StartDate     string
	EndDate       string
	Status        string
	Version       int64
	CreatedAt     string
	UpdatedAt     string
}

type IdentityHandlerUserMutation struct {
	Operation       IdentityHandlerOperation
	User            IdentityUser
	ExpectedVersion int64
	LoginMode       IdentityHandlerLoginMode
}

type IdentityHandlerProfileBindingMutation struct {
	BindingKey          string
	ObjectKey           string
	ProfileID           string
	ExpectedVersion     int64
	CreateProfileFields map[string]any
	Reason              string
	ApprovalID          string
}

type IdentityHandlerDeliveryRequest struct {
	User           IdentityHandlerUserMutation
	RoleKeys       []string
	ProfileBinding *IdentityHandlerProfileBindingMutation
}

type IdentityHandlerProfileBinding struct {
	BindingKey     string
	ObjectKey      string
	ProfileID      string
	IdentityUserID string
	Status         string
	Version        int64
}

// IdentityHandlerProfileBindingSelector names one generated, statically
// granted profile binding plus the business profile selected by project code.
// Runtime verifies BindingKey/ObjectKey against the Handler descriptor before
// forwarding the selector to Identity.
type IdentityHandlerProfileBindingSelector struct {
	BindingKey string
	ObjectKey  string
	ProfileID  string
}

// IdentityHandlerDeliveryResult deliberately has no credential field. Runtime
// keeps the one-time credential in volatile memory and emits it only after the
// host transaction commit succeeds.
type IdentityHandlerDeliveryResult struct {
	DeliveryID               string
	User                     IdentityUser
	RoleKeys                 []string
	ProfileBinding           *IdentityHandlerProfileBinding
	RevokedSessions          int
	Replayed                 bool
	InitialCredentialPending bool
}

type IdentityBoundIdentity struct {
	UserID                string
	DisplayName           string
	Status                string
	Active                bool
	Version               int64
	OrganizationID        string
	OrganizationPath      string
	OrganizationScopeIDs  []string
	SupportOrganizationID string
	SupportOrgScopeIDs    []string
	ManagerUserID         string
	ReportingPath         string
	ReportingScopeUserIDs []string
	RoleKeys              []string
	ProfileBinding        *IdentityHandlerProfileBinding
}

type IdentityHandlerDeliveryExecution interface {
	DeliverIdentity(context.Context, IdentityHandlerDeliveryRequest) (IdentityHandlerDeliveryResult, error)
	ResolveBoundIdentity(context.Context, string) (IdentityBoundIdentity, error)
}

// IdentityHandlerProfileResolutionExecution is separate from the original
// delivery interface so existing consumers that only resolve user facts stay
// source compatible. Generated bindings use it only for a statically authored
// profile binding.
type IdentityHandlerProfileResolutionExecution interface {
	ResolveBoundIdentityProfile(context.Context, string, IdentityHandlerProfileBindingSelector) (IdentityBoundIdentity, error)
}

var ErrIdentityHandlerDeliveryUnavailable = errors.New("identity handler delivery is unavailable")

func DeliverIdentity(ctx context.Context, execution ActionExecution, request IdentityHandlerDeliveryRequest) (IdentityHandlerDeliveryResult, error) {
	capability, ok := execution.(IdentityHandlerDeliveryExecution)
	if !ok {
		return IdentityHandlerDeliveryResult{}, ErrIdentityHandlerDeliveryUnavailable
	}
	return capability.DeliverIdentity(ctx, request)
}

func ResolveBoundIdentity(ctx context.Context, execution ActionExecution, userID string) (IdentityBoundIdentity, error) {
	capability, ok := execution.(IdentityHandlerDeliveryExecution)
	if !ok {
		return IdentityBoundIdentity{}, ErrIdentityHandlerDeliveryUnavailable
	}
	return capability.ResolveBoundIdentity(ctx, userID)
}

func ResolveBoundIdentityProfile(ctx context.Context, execution ActionExecution, userID string, selector IdentityHandlerProfileBindingSelector) (IdentityBoundIdentity, error) {
	capability, ok := execution.(IdentityHandlerProfileResolutionExecution)
	if !ok {
		return IdentityBoundIdentity{}, ErrIdentityHandlerDeliveryUnavailable
	}
	return capability.ResolveBoundIdentityProfile(ctx, userID, selector)
}
