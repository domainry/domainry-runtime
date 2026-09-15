package action

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ActionInvocationReceipt deliberately cannot contain raw outputs, record
// values, audit evidence, credentials or triggered workflow execution details.
type ActionInvocationReceipt struct {
	Found                                                           bool
	Status                                                          string
	InvocationID                                                    string
	CreatedRecords, UpdatedRecords, DeletedRecords, RestoredRecords []actionmodel.ActionObjectRecordRef
}

func receiptReadDenied() error {
	return apperror.New(apperror.KindForbidden, "backend.action.receipt_read_denied", nil, nil)
}

func (s *ActionApplicationService) receiptRecordReadable(ctx context.Context, objectKey, recordID string, p principalmodel.Principal) error {
	if objectKey == "" || recordID == "" || s.dependencies.ReceiptRecordReadable == nil {
		return receiptReadDenied()
	}
	allowed, err := s.dependencies.ReceiptRecordReadable(ctx, objectKey, recordID, p)
	if err != nil || !allowed {
		return receiptReadDenied()
	}
	return nil
}

func (s *ActionApplicationService) authorizeInvocationReceipt(ctx context.Context, in actionmodel.ActionInvocation, action definitionmodel.ActionSchema) error {
	return s.authorizeInvocationReceiptOwner(ctx, in, action, in.Principal.UserID)
}

// Receipt discovery discloses only the current published contract. It uses the
// original owner for receipt scope and the actual reader for object permission;
// it never queries an invocation, acquires a claim or authorizes execution.
func (s *ActionApplicationService) AgentSharedActionReceiptDefinition(ctx context.Context, key string, reader, producer principalmodel.Principal) (definitionmodel.ActionSchema, error) {
	if !reader.Known || !producer.Known || reader.UserID == "" || producer.UserID == "" || reader.WorkspaceID == "" || producer.WorkspaceID != reader.WorkspaceID {
		return definitionmodel.ActionSchema{}, receiptReadDenied()
	}
	entry, ok := s.dependencies.Catalog.Entry(key)
	if !ok {
		return definitionmodel.ActionSchema{}, receiptReadDenied()
	}
	action := entry.Definition
	in := actionmodel.ActionInvocation{Principal: reader, ObjectKey: action.ObjectKey, ActionKey: action.Key}
	if err := s.authorizeInvocationReceiptOwner(ctx, in, action, producer.UserID); err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	return action, nil
}

func (s *ActionApplicationService) authorizeInvocationReceiptOwner(ctx context.Context, in actionmodel.ActionInvocation, action definitionmodel.ActionSchema, ownerUserID string) error {
	p := in.Principal
	if !p.Known || p.UserID == "" || p.AccessBundle == nil || s.dependencies.Authorization.ObjectForAction == nil {
		return receiptReadDenied()
	}
	key := actioncontract.ReceiptReadActionKey(action.Key)
	decision, err := evaluator.Evaluate(*p.AccessBundle, identitysdk.AccessRequest{ObjectKey: strings.TrimSuffix(key, ".read"), Action: "read"}, identitysdk.ResourceFacts{
		"owner_user_id": ownerUserID, "workspace_id": p.WorkspaceID,
		"object_key": in.ObjectKey, "record_id": in.RecordID, "action_key": in.ActionKey,
	}, time.Now().UTC())
	if err != nil || !decision.Allowed {
		return receiptReadDenied()
	}
	if _, err := s.dependencies.Authorization.ObjectForAction(p, action.ObjectKey, "read"); err != nil {
		return receiptReadDenied()
	}
	if in.RecordID != "" {
		return s.receiptRecordReadable(ctx, in.ObjectKey, in.RecordID, p)
	}
	return nil
}

// ReadInvocationReceipt uses the original immutable ledger with the actual
// reader's identity. It never grants execution, consumes assurance, claims a
// lease or invokes a handler. Current read scope applies to ALL references,
// including references a consumer may later truncate from its presentation.
func (s *ActionApplicationService) ReadInvocationReceipt(ctx context.Context, in actionmodel.ActionInvocation) (ActionInvocationReceipt, error) {
	// Reading uses Principal as the actual reader, never command run-as grants.
	if in.RunAs.Known || !in.Principal.Known || in.Actor.Known && (in.Actor.UserID != in.Principal.UserID || in.Actor.WorkspaceID != in.Principal.WorkspaceID) {
		return ActionInvocationReceipt{}, receiptReadDenied()
	}
	in = ActionNormalizeInvocation(in)
	stored, err := s.inspectInvocation(ctx, in, true)
	return s.projectInvocationReceipt(ctx, in, stored, err)
}

// Producer locates the immutable execution ledger. Principal remains the
// actual reader, whose receipt policy is evaluated against the original actor.
func (s *ActionApplicationService) ReadSharedInvocationReceipt(ctx context.Context, in actionmodel.ActionInvocation, producer principalmodel.Principal) (ActionInvocationReceipt, error) {
	if in.RunAs.Known || !in.Principal.Known || !producer.Known || in.Principal.UserID == "" || producer.UserID == "" || in.Principal.WorkspaceID == "" || producer.WorkspaceID != in.Principal.WorkspaceID || in.Actor.Known && (in.Actor.UserID != producer.UserID || in.Actor.WorkspaceID != producer.WorkspaceID) {
		return ActionInvocationReceipt{}, receiptReadDenied()
	}
	in.Actor = producer
	in = ActionNormalizeInvocation(in)
	stored, err := s.inspectInvocationForProducer(ctx, in, true, producer)
	return s.projectInvocationReceipt(ctx, in, stored, err)
}

func (s *ActionApplicationService) projectInvocationReceipt(ctx context.Context, in actionmodel.ActionInvocation, stored ActionInvocationInspection, err error) (ActionInvocationReceipt, error) {
	if err != nil {
		return ActionInvocationReceipt{}, err
	}
	out := ActionInvocationReceipt{Found: stored.Found, Status: stored.Status}
	if !stored.Found || stored.Status != string(idempotency.StatusSucceeded) {
		return out, nil
	}
	result := stored.Result
	if result.InvocationID != in.IdempotencyKey || (result.Record == nil) == (result.Object == nil) {
		return ActionInvocationReceipt{}, receiptReadDenied()
	}
	out.InvocationID = result.InvocationID
	if result.Record != nil {
		r := result.Record
		if r.ActionKey != in.ActionKey || r.ObjectKey != in.ObjectKey || r.RecordID != in.RecordID {
			return ActionInvocationReceipt{}, receiptReadDenied()
		}
		out.CreatedRecords, out.UpdatedRecords, out.DeletedRecords, out.RestoredRecords = r.CreatedRecords, r.UpdatedRecords, r.DeletedRecords, r.RestoredRecords
	} else {
		r := result.Object
		if r.ActionKey != in.ActionKey || r.ObjectKey != in.ObjectKey {
			return ActionInvocationReceipt{}, receiptReadDenied()
		}
		out.CreatedRecords, out.UpdatedRecords, out.DeletedRecords, out.RestoredRecords = r.CreatedRecords, r.UpdatedRecords, r.DeletedRecords, r.RestoredRecords
	}
	for _, refs := range [][]actionmodel.ActionObjectRecordRef{out.CreatedRecords, out.UpdatedRecords, out.DeletedRecords, out.RestoredRecords} {
		for _, ref := range refs {
			if err := s.receiptRecordReadable(ctx, ref.ObjectKey, ref.RecordID, in.Principal); err != nil {
				return ActionInvocationReceipt{}, err
			}
		}
	}
	return out, nil
}
