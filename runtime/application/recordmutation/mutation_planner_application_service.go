package recordmutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type MutationMetadataRevisionResolver func(context.Context, principalmodel.Principal) (string, error)

type MutationInvocation struct {
	Source            transactionmodel.MutationSource
	ActionKey         string
	ActionResource    string
	ActionOperation   string
	WorkflowKey       string
	AutomationKey     string
	IdempotencyKey    string
	CausationID       string
	EffectAuthority   map[string][]string
	AssuranceEvidence map[string]string
	WorkflowTriggers  []string
}

type mutationInvocationContextKey struct{}
type mutationPredicatesContextKey struct{}

func WithMutationInvocation(ctx context.Context, invocation MutationInvocation) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, mutationInvocationContextKey{}, invocation)
}

func MutationInvocationFromContext(ctx context.Context) (MutationInvocation, bool) {
	if ctx == nil {
		return MutationInvocation{}, false
	}
	invocation, ok := ctx.Value(mutationInvocationContextKey{}).(MutationInvocation)
	return invocation, ok
}

func WithMutationPredicates(ctx context.Context, predicates []transactionmodel.MutationPredicate) context.Context {
	if ctx == nil {
		return nil
	}
	cloned := append([]transactionmodel.MutationPredicate(nil), predicates...)
	return context.WithValue(ctx, mutationPredicatesContextKey{}, cloned)
}

func MutationPredicatesFromContext(ctx context.Context) []transactionmodel.MutationPredicate {
	if ctx == nil {
		return nil
	}
	predicates, _ := ctx.Value(mutationPredicatesContextKey{}).([]transactionmodel.MutationPredicate)
	return append([]transactionmodel.MutationPredicate(nil), predicates...)
}

type MutationPlannerApplicationService struct {
	resolveRevision MutationMetadataRevisionResolver
}

func NewMutationPlannerApplicationService(resolveRevision MutationMetadataRevisionResolver) *MutationPlannerApplicationService {
	return &MutationPlannerApplicationService{resolveRevision: resolveRevision}
}

func (s *MutationPlannerApplicationService) Plan(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	switch strings.TrimSpace(commit.Operation) {
	case "create":
		return s.PlanCreate(ctx, principal, commit, before)
	case "update":
		return s.PlanUpdate(ctx, principal, commit, before)
	case "delete":
		return s.PlanDelete(ctx, principal, commit, before)
	case "restore":
		return s.PlanRestore(ctx, principal, commit, before)
	default:
		return transactionmodel.NewMutationPlan(transactionmodel.MutationContext{}, commit, before)
	}
}

func (s *MutationPlannerApplicationService) PlanCreate(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	commit.Operation = "create"
	return s.planCanonical(ctx, principal, commit, before)
}

func (s *MutationPlannerApplicationService) PlanUpdate(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	commit.Operation = "update"
	return s.planCanonical(ctx, principal, commit, before)
}

func (s *MutationPlannerApplicationService) PlanDelete(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	commit.Operation = "delete"
	return s.planCanonical(ctx, principal, commit, before)
}

func (s *MutationPlannerApplicationService) PlanRestore(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	commit.Operation = "restore"
	return s.planCanonical(ctx, principal, commit, before)
}

func (s *MutationPlannerApplicationService) planCanonical(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	if err := ctx.Err(); err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	invocation, found := MutationInvocationFromContext(ctx)
	if !found || invocation.Source == "" {
		invocation.Source = transactionmodel.MutationSourceHTTP
	}
	if transactionmodel.MutationObjectWritePolicy(commit.Object) == transactionmodel.ObjectWritePolicyActionOnly && invocation.Source != transactionmodel.MutationSourceAction && invocation.Source != transactionmodel.MutationSourceInternal {
		return transactionmodel.MutationPlan{}, &MutationPlannerError{Code: "backend.mutation.action_required", Field: commit.Object.Key}
	}
	if err := recordpolicy.RecordValidateLifecycleMutation(commit.Object, commit.Operation, before); err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	revision, err := s.metadataRevision(ctx, principal, commit)
	if err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	requestID := firstMutationCoordinate(principal.RequestID, requestcontext.RequestID(ctx))
	correlationID := firstMutationCoordinate(requestcontext.CorrelationID(ctx), requestID)
	if correlationID == "" {
		correlationID = requestcontext.NewRequestID()
	}
	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
		WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey,
		Permissions: principal.PermissionKeys(),
		Source:      invocation.Source, ActionKey: invocation.ActionKey, WorkflowKey: invocation.WorkflowKey,
		AutomationKey: invocation.AutomationKey, RequestID: requestID, IdempotencyKey: invocation.IdempotencyKey,
		CorrelationID: correlationID, CausationID: invocation.CausationID, ApplicationSchemaRevision: revision,
		EffectAuthority: invocation.EffectAuthority, AssuranceEvidence: invocation.AssuranceEvidence,
	})
	if err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	return transactionmodel.NewMutationPlan(mutationContext, commit, before)
}

func (s *MutationPlannerApplicationService) metadataRevision(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit) (string, error) {
	if s != nil && s.resolveRevision != nil {
		revision, err := s.resolveRevision(ctx, principal)
		if err != nil {
			return "", err
		}
		if revision = strings.TrimSpace(revision); revision != "" {
			return revision, nil
		}
		return "", &MutationPlannerError{Code: "backend.mutation.metadata_revision_unavailable"}
	}
	payload, err := json.Marshal(commit.Object)
	if err != nil {
		return "", &MutationPlannerError{Code: "backend.mutation.metadata_revision_unavailable", Err: err}
	}
	digest := sha256.Sum256(payload)
	return "object:" + hex.EncodeToString(digest[:]), nil
}

type MutationPlannerError struct {
	Code  string
	Field string
	Err   error
}

func (e *MutationPlannerError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Field)
	}
	return e.Code
}

func (e *MutationPlannerError) Unwrap() error { return e.Err }

func firstMutationCoordinate(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
