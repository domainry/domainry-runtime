package agenthost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const businessPolicyProjectionLimit = 32

// This is a memo of pure schema/policy computation, not an authorization or
// business-result cache. Every caller still resolves its current principal and
// schema, and every saved row still passes through the ordinary record port.
// These values are private, immutable, and never returned through a public API.
type businessPolicyProjection struct {
	objects      []definitionmodel.ObjectSchema
	policyDigest string
	readDigest   string
	expiresAt    time.Time
}

func (h *ConversationBusinessHost) businessProjection(ctx context.Context, p principalmodel.Principal) *businessPolicyProjection {
	snapshot := h.schema.ForPrincipal(ctx, p)
	key, expiresAt, cacheable := businessProjectionKey(snapshot, p, time.Now())
	if ctx.Err() != nil || !cacheable {
		return buildBusinessProjection(snapshot, p)
	}

	// Serialize misses: concurrent event/history readers of the same policy
	// must not each repeat the all-object/all-field projection.
	h.projectionMu.Lock()
	defer h.projectionMu.Unlock()
	if entry := h.projections[key]; entry != nil && time.Now().Before(entry.expiresAt) {
		return entry
	}
	entry := buildBusinessProjection(snapshot, p)
	if ctx.Err() != nil || !time.Now().Before(expiresAt) {
		return entry
	}
	entry.expiresAt = expiresAt
	if h.projections == nil || len(h.projections) >= businessPolicyProjectionLimit {
		h.projections = make(map[string]*businessPolicyProjection)
	}
	h.projections[key] = entry
	return entry
}

func buildBusinessProjection(snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal) *businessPolicyProjection {
	entry := &businessPolicyProjection{objects: readableBusinessObjects(snapshot, p)}
	if p.AccessBundle != nil {
		entry.policyDigest = businessPolicyDigest(p, snapshot.Objects)
		entry.readDigest = businessReadPolicyDigest(p, entry.objects)
	}
	return entry
}

func businessProjectionKey(snapshot appschemamodel.ApplicationSchemaSnapshot, p principalmodel.Principal, now time.Time) (string, time.Time, bool) {
	// SchemaHash is the Runtime owner's canonical hash of the current immutable
	// projected snapshot. A provider without that contract is evaluated afresh.
	if snapshot.SchemaHash == "" || p.AccessBundle == nil || !p.Known {
		return "", time.Time{}, false
	}
	expiresAt := p.AccessBundle.ExpiresAt
	if !now.Before(expiresAt) || (!p.AuthorizationEvaluatedAt.IsZero() && !p.AuthorizationEvaluatedAt.Before(expiresAt)) {
		return "", expiresAt, false
	}
	// Hash the complete current principal, including the actual policy content,
	// revision, subject/workspace, role, system scope/capabilities and business
	// claims. Revision alone is not a sufficient invalidation key. Only expiry
	// is kept on the entry, so an equivalent refreshed bundle cannot extend the
	// lifetime of an older cached entry. Include evaluation time explicitly:
	// Principal intentionally omits it from ordinary JSON serialization.
	bundle := *p.AccessBundle
	bundle.ExpiresAt = time.Time{}
	p.AccessBundle = &bundle
	raw, err := json.Marshal(struct {
		Principal          principalmodel.Principal
		AccessBundle       identitysdk.AccessBundle
		SchemaHash         string
		EvaluationTime     time.Time
		SystemCapabilities []string
	}{p, bundle, snapshot.SchemaHash, p.AuthorizationEvaluatedAt, p.SystemCapabilities})
	if err != nil {
		return "", expiresAt, false
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), expiresAt, true
}
