package agenthost

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

// The host supplies a persistent, purpose-derived secret; no module-owned
// database, process-local signing key or model-provided credential is created.
// Rotation invalidates old proofs. Callers without a key retain exact rereads.
func WithConversationBusinessEvidenceKey(key []byte) ConversationBusinessHostOption {
	owned := append([]byte(nil), key...)
	return func(h *ConversationBusinessHost) error {
		if len(owned) < sha256.Size {
			return fmt.Errorf("conversation business evidence key must contain at least 32 bytes")
		}
		h.evidenceKey = append([]byte(nil), owned...)
		return nil
	}
}

func (h *ConversationBusinessHost) businessPolicyDigest(ctx context.Context, p principalmodel.Principal) string {
	bundle := *p.AccessBundle
	// Resolver expiry and revision identify a resolution, not its effective
	// policy. Current Identity is resolved on every check, including after a
	// revoke/restore. All actual grants, subject scopes and rules remain bound.
	bundle.AuthorizationRevision = ""
	bundle.ExpiresAt = time.Time{}
	return conversationBusinessDigest([]any{bundle, recordpolicy.RecordSDKEvaluationContext(p), h.schema.ForPrincipal(ctx, p).Objects})
}

func (h *ConversationBusinessHost) businessEvidenceMAC(e agentsdk.ConversationBusinessEvidence, policy string) []byte {
	e.HostProof = ""
	raw, _ := json.Marshal(e)
	mac := hmac.New(sha256.New, h.evidenceKey)
	_, _ = mac.Write([]byte("domainry-conversation-business-snapshot-v1:" + policy + ":"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func (h *ConversationBusinessHost) SealBusinessEvidence(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) (string, error) {
	if e.Operation == "workflow_get" {
		return h.sealWorkflowEvidence(ctx, e, a)
	}
	if len(h.evidenceKey) == 0 || e.Operation == "business_catalog" {
		return "", nil
	}
	if e.HostProof != "" {
		return "", conversationBusinessError("forbidden")
	}
	p, err := h.principal(ctx, a)
	if err != nil {
		return "", err
	}
	policy := h.businessPolicyDigest(ctx, p)
	// Never attest arbitrary caller content. Exact rereading also closes races
	// between obtaining a result and issuing its integrity proof.
	if err := h.RevalidateBusiness(ctx, e, a); err != nil {
		return "", err
	}
	p, err = h.principal(ctx, a)
	if err != nil || policy != h.businessPolicyDigest(ctx, p) {
		return "", conversationBusinessError("forbidden")
	}
	return "1:" + policy + ":" + hex.EncodeToString(h.businessEvidenceMAC(e, policy)), nil
}

func (h *ConversationBusinessHost) revalidateBusinessSnapshot(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	parts := strings.Split(e.HostProof, ":")
	if len(h.evidenceKey) < sha256.Size || len(parts) != 3 || parts[0] != "1" || len(parts[1]) != 64 || len(parts[2]) != 64 {
		return conversationBusinessError("forbidden")
	}
	mac, err := hex.DecodeString(parts[2])
	if err != nil || !hmac.Equal(mac, h.businessEvidenceMAC(e, parts[1])) {
		return conversationBusinessError("forbidden")
	}
	p, err := h.principal(ctx, a)
	if err != nil {
		return err
	}
	if parts[1] != h.businessPolicyDigest(ctx, p) {
		// A changed effective policy cannot authorize an old snapshot merely
		// because some rows are still readable. Fall back to the full result.
		return h.revalidateBusinessExact(ctx, e, a)
	}
	var objectKey string
	var saved []agentsdk.ConversationBusinessRecord
	switch e.Operation {
	case "get_record":
		var q agentsdk.ConversationBusinessGet
		var record agentsdk.ConversationBusinessRecord
		if json.Unmarshal(e.Input, &q) != nil || json.Unmarshal(e.Data, &record) != nil {
			return conversationBusinessError("forbidden")
		}
		objectKey, saved = q.ObjectKey, []agentsdk.ConversationBusinessRecord{record}
	case "query_records":
		var q agentsdk.ConversationBusinessQuery
		var page agentsdk.ConversationBusinessRecordPage
		if json.Unmarshal(e.Input, &q) != nil || json.Unmarshal(e.Data, &page) != nil {
			return conversationBusinessError("forbidden")
		}
		// Validate today's query scope, filter/sort fields and cursor as usual.
		// Its new rows/count are not substituted into the historical snapshot.
		if _, err := h.QueryBusinessRecords(ctx, q, a); err != nil {
			return err
		}
		objectKey, saved = q.ObjectKey, page.Items
	case "query_related_records":
		var q agentsdk.ConversationBusinessRelatedQuery
		var page agentsdk.ConversationBusinessRelatedPage
		if json.Unmarshal(e.Input, &q) != nil || json.Unmarshal(e.Data, &page) != nil {
			return conversationBusinessError("forbidden")
		}
		current, err := h.QueryRelatedBusinessRecords(ctx, q, a)
		if err != nil {
			return err
		}
		if current.ObjectKey != page.ObjectKey {
			return conversationBusinessError("forbidden")
		}
		objectKey, saved = page.ObjectKey, page.Items
	default:
		return h.revalidateBusinessExact(ctx, e, a)
	}
	// A row may leave the query after a normal update. Recheck each saved ID,
	// not just today's first page: owner changes, deletion and row policies
	// must still revoke the old result even if the overall policy is unchanged.
	for _, record := range saved {
		fields := make([]string, 0, len(record.Data))
		for field := range record.Data {
			fields = append(fields, field)
		}
		current, err := h.GetBusinessRecord(ctx, agentsdk.ConversationBusinessGet{ObjectKey: objectKey, RecordID: record.ID, Fields: fields}, a)
		if err != nil {
			return err
		}
		for field, value := range record.Data {
			// Conditional hiding/masking can change as row facts change even
			// with an identical policy. Preserve such a value only when the
			// current authorized projection returns the exact saved value.
			if recordpolicy.RecordFieldRequiresPolicyEvaluation(p, objectKey, field, "read") && !bytes.Equal(value, current.Data[field]) {
				return conversationBusinessError("forbidden")
			}
		}
	}
	p, err = h.principal(ctx, a)
	if err != nil || parts[1] != h.businessPolicyDigest(ctx, p) {
		return conversationBusinessError("forbidden")
	}
	return nil
}

var _ agentsdk.ConversationBusinessEvidenceSealer = (*ConversationBusinessHost)(nil)
