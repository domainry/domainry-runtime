package runtimeext

import (
	"context"
	"errors"
	"strings"
)

type AccountErasureOperation string

const (
	AccountErasureStage AccountErasureOperation = "stage"
	AccountErasureGet   AccountErasureOperation = "get"
)

// AccountErasureCapability fixes the profile and the business request proving
// the subject's intent. Runtime resolves its fields from published metadata.
type AccountErasureCapability struct {
	Operations            []AccountErasureOperation
	ProfileBinding        IdentityProfileBindingCapability
	RequestObjectKey      string
	RequestProfileField   string
	RequestRequesterField string
}

func (c AccountErasureCapability) Valid() bool {
	if len(c.Operations) == 0 {
		return false
	}
	seen := map[AccountErasureOperation]bool{}
	for _, op := range c.Operations {
		if op != AccountErasureStage && op != AccountErasureGet || seen[op] {
			return false
		}
		seen[op] = true
	}
	for _, key := range []string{c.ProfileBinding.BindingKey, c.ProfileBinding.ObjectKey, c.RequestObjectKey, c.RequestProfileField, c.RequestRequesterField} {
		if strings.TrimSpace(key) != key || !handlerFieldIdentityPattern.MatchString(key) {
			return false
		}
	}
	return c.RequestProfileField != c.RequestRequesterField
}

type AccountErasureStageRequest struct {
	ProfileID               string
	ApprovalID              string
	ExpectedIdentityVersion int64
	ExpectedBindingVersion  int64
}

type AccountErasureGetRequest struct {
	RequestID string
	ProfileID string
}

// Completed is true only after every source owner succeeds. BackupPending is
// separate because the deletion registration must survive backup restoration.
type AccountErasureReceipt struct {
	RequestID        string
	Status           string
	Completed        bool
	BackupPending    bool
	ExecutionAttempt int64
	UpdatedAt        string
}

type AccountErasureExecution interface {
	StageAccountErasure(context.Context, AccountErasureStageRequest) (AccountErasureReceipt, error)
	GetAccountErasure(context.Context, AccountErasureGetRequest) (AccountErasureReceipt, error)
}

var ErrAccountErasureUnavailable = errors.New("account erasure is unavailable")

func StageAccountErasure(ctx context.Context, execution ActionExecution, request AccountErasureStageRequest) (AccountErasureReceipt, error) {
	capability, ok := execution.(AccountErasureExecution)
	if !ok {
		return AccountErasureReceipt{}, ErrAccountErasureUnavailable
	}
	return capability.StageAccountErasure(ctx, request)
}

func GetAccountErasure(ctx context.Context, execution ActionExecution, request AccountErasureGetRequest) (AccountErasureReceipt, error) {
	capability, ok := execution.(AccountErasureExecution)
	if !ok {
		return AccountErasureReceipt{}, ErrAccountErasureUnavailable
	}
	return capability.GetAccountErasure(ctx, request)
}
