package businessseed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	businessseedvalidation "github.com/domainry/domainry-runtime/runtime/domain/businessseed/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type BusinessSeedAuthoringProvenanceStore interface {
	GetSeedProvenance(context.Context, string) (businessseedmodel.BusinessSeedProvenance, bool, error)
	UpsertSeedProvenance(context.Context, businessseedmodel.BusinessSeedProvenance) error
}

type BusinessSeedAuthoringDependencies struct {
	Records    recordrepository.RecordRepository
	Provenance BusinessSeedAuthoringProvenanceStore
	Objects    func() []definitionmodel.ObjectSchema
	Audit      func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	Now        func() time.Time
}

type BusinessSeedAuthoringApplicationService struct {
	dependencies BusinessSeedAuthoringDependencies
}

func NewBusinessSeedAuthoringApplicationService(dependencies BusinessSeedAuthoringDependencies) *BusinessSeedAuthoringApplicationService {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &BusinessSeedAuthoringApplicationService{dependencies: dependencies}
}

func (s *BusinessSeedAuthoringApplicationService) Validate(ctx context.Context, seedKey string, request businessseedmodel.SeedRecordAuthoringRequest, principal principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	return s.prepare(ctx, seedKey, request, principal)
}

// Get returns the single immutable materialization owned by seedKey. A seed
// cannot acquire a second content hash; callers use Versions to consume the
// same fact through the uniform direct-authoring lifecycle contract.
func (s *BusinessSeedAuthoringApplicationService) Get(ctx context.Context, seedKey string, principal principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	if s == nil || s.dependencies.Records == nil || s.dependencies.Provenance == nil || s.dependencies.Objects == nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindUnavailable, "backend.business_seed.authoring_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	if err := s.authorizeRead(principal); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	seedKey = strings.TrimSpace(seedKey)
	if err := businessseedvalidation.ValidateBusinessSeedKey(seedKey); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	provenance, found, err := s.dependencies.Provenance.GetSeedProvenance(ctx, seedKey)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	if !found {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindNotFound, "backend.business_seed.not_found", "seed_key", seedKey)
	}
	object, found := s.object(provenance.ObjectKey)
	if !found {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.object_not_found", "object_key", provenance.ObjectKey)
	}
	record, found, err := s.dependencies.Records.GetRecord(ctx, principalmodel.InstallationWorkspaceID, object, provenance.RecordID)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	if !found {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.provenance_orphaned", "seed_key", seedKey)
	}
	return businessseedmodel.SeedRecordAuthoringResult{
		SeedKey: provenance.SeedKey, ObjectKey: provenance.ObjectKey, RecordID: provenance.RecordID, Data: record.Data,
		SourceKind: provenance.SourceKind, SourceID: provenance.SourceID, ContentHash: provenance.ContentHash, Replayed: true,
	}, nil
}

func (s *BusinessSeedAuthoringApplicationService) Versions(ctx context.Context, seedKey string, principal principalmodel.Principal) ([]businessseedmodel.SeedRecordAuthoringResult, error) {
	materialization, err := s.Get(ctx, seedKey, principal)
	if err != nil {
		return nil, err
	}
	return []businessseedmodel.SeedRecordAuthoringResult{materialization}, nil
}

func (s *BusinessSeedAuthoringApplicationService) AuthorizeSeedRecordUpsert(principal principalmodel.Principal) error {
	return s.authorizeRead(principal)
}

func (s *BusinessSeedAuthoringApplicationService) SeedRecordAuthoringHash(ctx context.Context, seedKey string, principal principalmodel.Principal) (string, bool, error) {
	materialization, err := s.Get(ctx, strings.TrimSpace(seedKey), principal)
	if err != nil {
		if apperror.KindOf(err) == apperror.KindNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	hash, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "seed.record.resource", ResourceType: "business_seed", TargetID: materialization.SeedKey,
		Payload: map[string]any{"seed_key": materialization.SeedKey, "object_key": materialization.ObjectKey, "record_id": materialization.RecordID, "data": materialization.Data, "source_kind": materialization.SourceKind, "source_id": materialization.SourceID, "content_hash": materialization.ContentHash},
	})
	return hash, true, err
}

func (s *BusinessSeedAuthoringApplicationService) Apply(ctx context.Context, seedKey string, request businessseedmodel.SeedRecordAuthoringRequest, principal principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	result, err := s.prepare(ctx, seedKey, request, principal)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	existing, found, err := s.dependencies.Provenance.GetSeedProvenance(ctx, result.SeedKey)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	object, _ := s.object(result.ObjectKey)
	if found {
		if existing.ObjectKey != result.ObjectKey || existing.ContentHash != result.ContentHash {
			return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.content_conflict", "seed_key", result.SeedKey)
		}
		record, recordFound, lookupErr := s.dependencies.Records.GetRecord(ctx, principalmodel.InstallationWorkspaceID, object, existing.RecordID)
		if lookupErr != nil {
			return businessseedmodel.SeedRecordAuthoringResult{}, lookupErr
		}
		if !recordFound {
			return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.provenance_orphaned", "seed_key", result.SeedKey)
		}
		result.RecordID, result.Data, result.Replayed = existing.RecordID, record.Data, true
		return result, nil
	}
	if _, recordFound, lookupErr := s.dependencies.Records.GetRecord(ctx, principalmodel.InstallationWorkspaceID, object, result.RecordID); lookupErr != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, lookupErr
	} else if recordFound {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.record_conflict", "record_id", result.RecordID)
	}
	now := s.dependencies.Now().UTC().Format(time.RFC3339Nano)
	record := recordmodel.Record{ID: result.RecordID, Data: result.Data, CreatedAt: now, UpdatedAt: now}
	if err := s.dependencies.Records.InsertRecord(ctx, principalmodel.InstallationWorkspaceID, object, record); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	provenance := businessseedmodel.BusinessSeedProvenance{SeedKey: result.SeedKey, ObjectKey: result.ObjectKey, RecordID: result.RecordID, SourceKind: result.SourceKind, SourceID: result.SourceID, ContentHash: result.ContentHash, MaterializedAt: now}
	if err := s.dependencies.Provenance.UpsertSeedProvenance(ctx, provenance); err != nil {
		if rollbackErr := s.dependencies.Records.DeleteRecord(ctx, principalmodel.InstallationWorkspaceID, object, result.RecordID); rollbackErr != nil {
			return businessseedmodel.SeedRecordAuthoringResult{}, fmt.Errorf("persist seed provenance: %w; compensate record: %v", err, rollbackErr)
		}
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	if s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, "business_seed_materialized", "business_seed", result.SeedKey, principal, "Materialized business seed "+result.SeedKey, nil, map[string]any{"object_key": result.ObjectKey, "record_id": result.RecordID, "content_hash": result.ContentHash}, nil)
	}
	return result, nil
}

func (s *BusinessSeedAuthoringApplicationService) prepare(ctx context.Context, seedKey string, request businessseedmodel.SeedRecordAuthoringRequest, principal principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	if s == nil || s.dependencies.Records == nil || s.dependencies.Provenance == nil || s.dependencies.Objects == nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindUnavailable, "backend.business_seed.authoring_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if !principal.Known || len(workspaceID) == 0 {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindBadRequest, "backend.workspace_scope_required")
	}
	if workspaceID != principalmodel.InstallationWorkspaceID {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.workspace_unsupported", "workspace_id", workspaceID)
	}
	if !businessSeedHasPermission(principal, "workspace.admin") {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindForbidden, "auth.permission_denied")
	}
	seedKey, request.SeedKey = strings.TrimSpace(seedKey), strings.TrimSpace(request.SeedKey)
	if request.SeedKey != "" && request.SeedKey != seedKey {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindBadRequest, "backend.business_seed.key_mismatch", "expected", seedKey, "actual", request.SeedKey)
	}
	if err := businessseedvalidation.ValidateBusinessSeedKey(seedKey); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	objectKey := strings.TrimSpace(request.ObjectKey)
	if strings.HasPrefix(objectKey, "identity_") {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindBadRequest, "backend.business_seed.identity_object_forbidden", "object_key", objectKey)
	}
	object, found := s.object(objectKey)
	if !found {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindNotFound, "backend.business_seed.object_not_found", "object_key", objectKey)
	}
	data, err := s.resolveReferences(ctx, request.Data)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, err
	}
	recordID := businessseedvalidation.BusinessSeedRecordID(objectKey, seedKey)
	recordpolicy.RecordApplyFieldDefaults(object, data)
	recordpolicy.RecordApplyAutoCodeDefaults(object, data, recordID)
	normalized, err := recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, apperror.FromError(apperror.KindBadRequest, err)
	}
	if err := recordvalidation.RecordValidateData(object, normalized, false); err != nil {
		return businessseedmodel.SeedRecordAuthoringResult{}, apperror.FromError(apperror.KindBadRequest, err)
	}
	// RecordNormalizeData has already reduced every supported field type to
	// JSON-safe scalars, so encoding cannot fail for this closed value domain.
	encoded, _ := json.Marshal(normalized)
	hash := sha256.Sum256(encoded)
	sourceKind := strings.TrimSpace(request.SourceKind)
	if sourceKind == "" {
		sourceKind = "builder_v4"
	}
	sourceID := strings.TrimSpace(request.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(principal.RequestID)
	}
	if sourceID == "" {
		return businessseedmodel.SeedRecordAuthoringResult{}, businessSeedAuthoringError(apperror.KindBadRequest, "backend.business_seed.source_id_required")
	}
	return businessseedmodel.SeedRecordAuthoringResult{SeedKey: seedKey, ObjectKey: objectKey, RecordID: recordID, Data: normalized, SourceKind: sourceKind, SourceID: sourceID, ContentHash: hex.EncodeToString(hash[:])}, nil
}

func (s *BusinessSeedAuthoringApplicationService) authorizeRead(principal principalmodel.Principal) error {
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if !principal.Known || workspaceID == "" {
		return businessSeedAuthoringError(apperror.KindBadRequest, "backend.workspace_scope_required")
	}
	if workspaceID != principalmodel.InstallationWorkspaceID {
		return businessSeedAuthoringError(apperror.KindConflict, "backend.business_seed.workspace_unsupported", "workspace_id", workspaceID)
	}
	if !businessSeedHasPermission(principal, "workspace.admin") {
		return businessSeedAuthoringError(apperror.KindForbidden, "auth.permission_denied")
	}
	return nil
}

func (s *BusinessSeedAuthoringApplicationService) resolveReferences(ctx context.Context, value map[string]any) (map[string]any, error) {
	resolved, err := s.resolveReferenceValue(ctx, value)
	if err != nil {
		return nil, err
	}
	// The input is always a map and resolveReferenceValue preserves container
	// shape, so this assertion is guaranteed by the private call contract.
	return resolved.(map[string]any), nil
}

func (s *BusinessSeedAuthoringApplicationService) resolveReferenceValue(ctx context.Context, value any) (any, error) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if key := strings.TrimSpace(strings.TrimPrefix(trimmed, "$record:")); key != trimmed && key != "" {
			provenance, found, err := s.dependencies.Provenance.GetSeedProvenance(ctx, key)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, businessSeedAuthoringError(apperror.KindNotFound, "backend.business_seed.reference_not_found", "seed_key", key)
			}
			return provenance.RecordID, nil
		}
		return typed, nil
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			resolved, err := s.resolveReferenceValue(ctx, item)
			if err != nil {
				return nil, err
			}
			result = append(result, resolved)
		}
		return result, nil
	case map[string]any:
		result := map[string]any{}
		for key, item := range typed {
			resolved, err := s.resolveReferenceValue(ctx, item)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	default:
		return typed, nil
	}
}

func (s *BusinessSeedAuthoringApplicationService) object(key string) (definitionmodel.ObjectSchema, bool) {
	if s.dependencies.Objects == nil {
		return definitionmodel.ObjectSchema{}, false
	}
	for _, object := range s.dependencies.Objects() {
		if strings.TrimSpace(object.Key) == strings.TrimSpace(key) && strings.TrimSpace(key) != "" {
			return object, true
		}
	}
	return definitionmodel.ObjectSchema{}, false
}

func businessSeedHasPermission(principal principalmodel.Principal, permission string) bool {
	return principal.HasExactPermission(permission)
}

func businessSeedAuthoringError(kind apperror.ErrorKind, code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		params[values[index]] = values[index+1]
	}
	if len(params) == 0 {
		params = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: params}
}
