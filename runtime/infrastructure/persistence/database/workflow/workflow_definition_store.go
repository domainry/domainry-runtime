package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	workflowDefinitionKind            = "workflow"
	workflowDefinitionSourceKind      = "workflow_management"
	workflowDefinitionContractVersion = "domainry-workflow-definition-v1"
	workflowVersionContractVersion    = "domainry-workflow-definition-version-v1"
	workflowVersionResourcePrefix     = "version:"
	workflowDefinitionMutationRetries = 64
)

type workflowDefinitionState struct {
	ContractVersion string                           `json:"contract_version"`
	Definition      workflowmodel.WorkflowDefinition `json:"definition"`
}

type workflowVersionState struct {
	ContractVersion       string                                  `json:"contract_version"`
	PublishIdempotencyKey string                                  `json:"publish_idempotency_key,omitempty"`
	Version               workflowmodel.WorkflowDefinitionVersion `json:"version"`
}

type storedWorkflowDefinition struct {
	metadata metadatasdk.Definition
	state    workflowDefinitionState
}

type storedWorkflowVersion struct {
	metadata metadatasdk.Definition
	state    workflowVersionState
}

type WorkflowDefinitionStore struct {
	store  *database.RuntimeStore
	shared metadatasdk.DefinitionStore
}

func NewWorkflowDefinitionStore(store *database.RuntimeStore) WorkflowDefinitionStore {
	var shared metadatasdk.DefinitionStore
	if store != nil && store.Metadata() != nil {
		shared = store.Metadata().DefinitionStore()
	}
	return WorkflowDefinitionStore{store: store, shared: shared}
}

func (r WorkflowDefinitionStore) validate() error {
	if r.store == nil || r.store.DB() == nil || r.shared == nil {
		return fmt.Errorf("Workflow shared Definition store is unavailable")
	}
	return nil
}

func (r WorkflowDefinitionStore) InsertDefinition(ctx context.Context, definition workflowmodel.WorkflowDefinition, draft workflowmodel.WorkflowDefinitionVersion) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(definition.ID) == "" || strings.TrimSpace(definition.Key) == "" || strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.DefinitionID) != strings.TrimSpace(definition.ID) {
		return fmt.Errorf("Workflow definition and draft identity are required")
	}
	tx, err := r.store.DB().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	sharedCtx := metadatamodulehost.WithExecutor(ctx, tx)
	if _, found, err := r.loadDefinitionByKey(sharedCtx, definition.Key); err != nil {
		return err
	} else if found {
		return fmt.Errorf("Workflow definition %q already exists", definition.Key)
	}
	if _, found, err := r.loadVersionByID(sharedCtx, draft.ID); err != nil {
		return err
	} else if found {
		return fmt.Errorf("Workflow definition version %q already exists", draft.ID)
	}
	definition.CurrentDraftVersionID = draft.ID
	definition.CurrentPublishedVersionID = ""
	if _, err := r.publishVersion(sharedCtx, metadatasdk.DefinitionNoCurrentVersion, workflowVersionState{ContractVersion: workflowVersionContractVersion, Version: draft}); err != nil {
		return err
	}
	if _, err := r.publishDefinition(sharedCtx, metadatasdk.DefinitionNoCurrentVersion, workflowDefinitionState{ContractVersion: workflowDefinitionContractVersion, Definition: definition}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r WorkflowDefinitionStore) GetDefinitionByKey(ctx context.Context, key string) (workflowmodel.WorkflowDefinition, bool, error) {
	if err := r.validate(); err != nil {
		return workflowmodel.WorkflowDefinition{}, false, err
	}
	stored, found, err := r.loadDefinitionByKey(ctx, key)
	if err != nil || !found {
		return workflowmodel.WorkflowDefinition{}, found, err
	}
	definition, err := r.hydrateDefinition(ctx, stored.state.Definition)
	return definition, err == nil, err
}

func (r WorkflowDefinitionStore) ListDefinitions(ctx context.Context) ([]workflowmodel.WorkflowDefinition, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	stored, err := r.listStoredDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make([]workflowmodel.WorkflowDefinition, 0, len(stored))
	for _, value := range stored {
		definition, err := r.hydrateDefinition(ctx, value.state.Definition)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Key == definitions[j].Key {
			return definitions[i].ID < definitions[j].ID
		}
		return definitions[i].Key < definitions[j].Key
	})
	return definitions, nil
}

func (r WorkflowDefinitionStore) GetVersion(ctx context.Context, versionID string) (workflowmodel.WorkflowDefinitionVersion, bool, error) {
	if err := r.validate(); err != nil {
		return workflowmodel.WorkflowDefinitionVersion{}, false, err
	}
	stored, found, err := r.loadVersionByID(ctx, versionID)
	if err != nil || !found {
		return workflowmodel.WorkflowDefinitionVersion{}, found, err
	}
	return stored.state.Version, true, nil
}

func (r WorkflowDefinitionStore) ListVersions(ctx context.Context, definitionID string) ([]workflowmodel.WorkflowDefinitionVersion, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	stored, err := r.listStoredVersions(ctx)
	if err != nil {
		return nil, err
	}
	definitionID = strings.TrimSpace(definitionID)
	versions := make([]workflowmodel.WorkflowDefinitionVersion, 0, len(stored))
	for _, value := range stored {
		if definitionID == "" || strings.TrimSpace(value.state.Version.DefinitionID) == definitionID {
			versions = append(versions, value.state.Version)
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Version == versions[j].Version {
			return versions[i].ID > versions[j].ID
		}
		return versions[i].Version > versions[j].Version
	})
	return versions, nil
}

func (r WorkflowDefinitionStore) UpdateDraft(ctx context.Context, version workflowmodel.WorkflowDefinitionVersion, expectedRevision int) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		stored, found, err := r.loadVersionByID(sharedCtx, version.ID)
		if err != nil || !found {
			return false, err
		}
		current := stored.state.Version
		if current.Status != workflowmodel.WorkflowVersionDraft || current.Revision != expectedRevision {
			return false, nil
		}
		current.Workflow = version.Workflow
		current.ValidationReport = version.ValidationReport
		current.Revision = expectedRevision + 1
		current.UpdatedAt = version.UpdatedAt
		_, err = r.publishVersion(sharedCtx, stored.metadata.CurrentVersionID, workflowVersionState{ContractVersion: workflowVersionContractVersion, Version: current})
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) InsertDraftVersion(ctx context.Context, definitionID string, draft workflowmodel.WorkflowDefinitionVersion) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		definition, found, err := r.loadDefinitionByID(sharedCtx, definitionID)
		if err != nil || !found {
			return false, err
		}
		if strings.TrimSpace(definition.state.Definition.CurrentDraftVersionID) != "" {
			return false, nil
		}
		if strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.DefinitionID) != strings.TrimSpace(definitionID) {
			return false, fmt.Errorf("Workflow draft identity does not match its definition")
		}
		if _, found, err := r.loadVersionByID(sharedCtx, draft.ID); err != nil {
			return false, err
		} else if found {
			return false, fmt.Errorf("Workflow definition version %q already exists", draft.ID)
		}
		versions, err := r.listStoredVersions(sharedCtx)
		if err != nil {
			return false, err
		}
		for _, value := range versions {
			if value.state.Version.DefinitionID == definitionID && value.state.Version.Version == draft.Version {
				return false, fmt.Errorf("Workflow definition version number %d already exists", draft.Version)
			}
		}
		if _, err := r.publishVersion(sharedCtx, metadatasdk.DefinitionNoCurrentVersion, workflowVersionState{ContractVersion: workflowVersionContractVersion, Version: draft}); err != nil {
			return false, err
		}
		definition.state.Definition.CurrentDraftVersionID = draft.ID
		definition.state.Definition.UpdatedAt = draft.UpdatedAt
		_, err = r.publishDefinition(sharedCtx, definition.metadata.CurrentVersionID, definition.state)
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) DeleteDraft(ctx context.Context, definitionID, versionID string) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		definition, found, err := r.loadDefinitionByID(sharedCtx, definitionID)
		if err != nil || !found {
			return false, err
		}
		version, found, err := r.loadVersionByID(sharedCtx, versionID)
		if err != nil || !found {
			return false, err
		}
		if version.state.Version.DefinitionID != definitionID || version.state.Version.Status != workflowmodel.WorkflowVersionDraft || definition.state.Definition.CurrentDraftVersionID != versionID {
			return false, nil
		}
		definition.state.Definition.CurrentDraftVersionID = ""
		if _, err := r.publishDefinition(sharedCtx, definition.metadata.CurrentVersionID, definition.state); err != nil {
			return false, err
		}
		err = r.shared.Disable(sharedCtx, metadatasdk.DefinitionDisableCommand{
			Owner: metadatasdk.DefinitionOwnerWorkflow, ResourceType: workflowDefinitionKind,
			ResourceKey: workflowVersionResourceKey(versionID), ExpectedCurrentVersionID: version.metadata.CurrentVersionID,
			DisabledBy: "system:workflow",
		})
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) PublishDraft(ctx context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion, idempotencyKey string) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		storedDefinition, found, err := r.loadDefinitionByID(sharedCtx, definition.ID)
		if err != nil || !found {
			return false, err
		}
		storedVersion, found, err := r.loadVersionByID(sharedCtx, version.ID)
		if err != nil || !found {
			return false, err
		}
		current := storedVersion.state.Version
		if storedDefinition.state.Definition.CurrentDraftVersionID != version.ID || current.DefinitionID != definition.ID || current.Status != workflowmodel.WorkflowVersionDraft || current.Revision != version.Revision || !workflowSchemasEqual(current.Workflow, version.Workflow) {
			return false, nil
		}
		versions, err := r.listStoredVersions(sharedCtx)
		if err != nil {
			return false, err
		}
		for _, value := range versions {
			candidate := value.state.Version
			if candidate.DefinitionID == definition.ID && candidate.ID != version.ID && strings.TrimSpace(candidate.PublishIdempotencyKey) == strings.TrimSpace(idempotencyKey) && strings.TrimSpace(idempotencyKey) != "" {
				return false, fmt.Errorf("Workflow publish idempotency key %q already exists", idempotencyKey)
			}
		}
		current.Status = workflowmodel.WorkflowVersionPublished
		current.ContentHash = version.ContentHash
		current.ValidationReport = version.ValidationReport
		current.PublishNote = version.PublishNote
		current.PublishedBy = version.PublishedBy
		current.PublishIdempotencyKey = idempotencyKey
		current.PublishedAt = version.PublishedAt
		current.UpdatedAt = version.UpdatedAt
		if _, err := r.publishVersion(sharedCtx, storedVersion.metadata.CurrentVersionID, workflowVersionState{ContractVersion: workflowVersionContractVersion, Version: current}); err != nil {
			return false, err
		}
		storedDefinition.state.Definition.CurrentDraftVersionID = ""
		storedDefinition.state.Definition.CurrentPublishedVersionID = version.ID
		storedDefinition.state.Definition.UpdatedAt = definition.UpdatedAt
		_, err = r.publishDefinition(sharedCtx, storedDefinition.metadata.CurrentVersionID, storedDefinition.state)
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) ArchiveVersion(ctx context.Context, definitionID, versionID, archivedAt string) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		definition, found, err := r.loadDefinitionByID(sharedCtx, definitionID)
		if err != nil || !found {
			return false, err
		}
		version, found, err := r.loadVersionByID(sharedCtx, versionID)
		if err != nil || !found {
			return false, err
		}
		if definition.state.Definition.CurrentPublishedVersionID == versionID || version.state.Version.DefinitionID != definitionID || version.state.Version.Status != workflowmodel.WorkflowVersionPublished {
			return false, nil
		}
		version.state.Version.Status = workflowmodel.WorkflowVersionArchived
		version.state.Version.ArchivedAt = archivedAt
		version.state.Version.UpdatedAt = archivedAt
		_, err = r.publishVersion(sharedCtx, version.metadata.CurrentVersionID, version.state)
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) SetDefinitionEnabled(ctx context.Context, definitionID string, enabled bool, updatedAt string) (bool, error) {
	return r.mutate(ctx, func(sharedCtx context.Context) (bool, error) {
		definition, found, err := r.loadDefinitionByID(sharedCtx, definitionID)
		if err != nil || !found {
			return false, err
		}
		definition.state.Definition.Enabled = enabled
		definition.state.Definition.UpdatedAt = updatedAt
		_, err = r.publishDefinition(sharedCtx, definition.metadata.CurrentVersionID, definition.state)
		return err == nil, err
	})
}

func (r WorkflowDefinitionStore) hydrateDefinition(ctx context.Context, definition workflowmodel.WorkflowDefinition) (workflowmodel.WorkflowDefinition, error) {
	definition.CurrentDraftVersion, definition.CurrentDraftStatus = 0, ""
	definition.CurrentPublishedVersion, definition.LastPublishedAt, definition.LastPublishedBy = 0, "", ""
	if definition.CurrentDraftVersionID != "" {
		version, found, err := r.loadVersionByID(ctx, definition.CurrentDraftVersionID)
		if err != nil {
			return workflowmodel.WorkflowDefinition{}, err
		}
		if !found || version.state.Version.DefinitionID != definition.ID {
			return workflowmodel.WorkflowDefinition{}, fmt.Errorf("Workflow definition %q references a missing draft", definition.Key)
		}
		definition.CurrentDraftVersion = version.state.Version.Version
		definition.CurrentDraftStatus = version.state.Version.Status
	}
	if definition.CurrentPublishedVersionID != "" {
		version, found, err := r.loadVersionByID(ctx, definition.CurrentPublishedVersionID)
		if err != nil {
			return workflowmodel.WorkflowDefinition{}, err
		}
		if !found || version.state.Version.DefinitionID != definition.ID {
			return workflowmodel.WorkflowDefinition{}, fmt.Errorf("Workflow definition %q references a missing published version", definition.Key)
		}
		definition.CurrentPublishedVersion = version.state.Version.Version
		definition.LastPublishedAt = version.state.Version.PublishedAt
		definition.LastPublishedBy = version.state.Version.PublishedBy
	}
	return definition, nil
}

func (r WorkflowDefinitionStore) loadDefinitionByKey(ctx context.Context, key string) (storedWorkflowDefinition, bool, error) {
	metadata, found, err := r.shared.Get(ctx, metadatasdk.DefinitionOwnerWorkflow, workflowDefinitionKind, workflowDefinitionResourceKey(key))
	if err != nil || !found {
		return storedWorkflowDefinition{}, found, err
	}
	state, err := decodeWorkflowDefinition(metadata)
	return storedWorkflowDefinition{metadata: metadata, state: state}, err == nil, err
}

func (r WorkflowDefinitionStore) loadDefinitionByID(ctx context.Context, definitionID string) (storedWorkflowDefinition, bool, error) {
	definitions, err := r.listStoredDefinitions(ctx)
	if err != nil {
		return storedWorkflowDefinition{}, false, err
	}
	definitionID = strings.TrimSpace(definitionID)
	for _, definition := range definitions {
		if definition.state.Definition.ID == definitionID {
			return definition, true, nil
		}
	}
	return storedWorkflowDefinition{}, false, nil
}

func (r WorkflowDefinitionStore) loadVersionByID(ctx context.Context, versionID string) (storedWorkflowVersion, bool, error) {
	metadata, found, err := r.shared.Get(ctx, metadatasdk.DefinitionOwnerWorkflow, workflowDefinitionKind, workflowVersionResourceKey(versionID))
	if err != nil || !found {
		return storedWorkflowVersion{}, found, err
	}
	state, err := decodeWorkflowVersion(metadata)
	return storedWorkflowVersion{metadata: metadata, state: state}, err == nil, err
}

func (r WorkflowDefinitionStore) listStoredDefinitions(ctx context.Context) ([]storedWorkflowDefinition, error) {
	definitions, err := r.shared.List(ctx, metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerWorkflow, ResourceType: workflowDefinitionKind})
	if err != nil {
		return nil, err
	}
	values := []storedWorkflowDefinition{}
	for _, definition := range definitions {
		if strings.HasPrefix(definition.ResourceKey, workflowVersionResourcePrefix) {
			continue
		}
		state, err := decodeWorkflowDefinition(definition)
		if err != nil {
			return nil, err
		}
		values = append(values, storedWorkflowDefinition{metadata: definition, state: state})
	}
	return values, nil
}

func (r WorkflowDefinitionStore) listStoredVersions(ctx context.Context) ([]storedWorkflowVersion, error) {
	definitions, err := r.shared.List(ctx, metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerWorkflow, ResourceType: workflowDefinitionKind})
	if err != nil {
		return nil, err
	}
	values := []storedWorkflowVersion{}
	for _, definition := range definitions {
		if !strings.HasPrefix(definition.ResourceKey, workflowVersionResourcePrefix) {
			continue
		}
		state, err := decodeWorkflowVersion(definition)
		if err != nil {
			return nil, err
		}
		values = append(values, storedWorkflowVersion{metadata: definition, state: state})
	}
	return values, nil
}

func decodeWorkflowDefinition(metadata metadatasdk.Definition) (workflowDefinitionState, error) {
	var state workflowDefinitionState
	if err := json.Unmarshal(metadata.Payload, &state); err != nil {
		return workflowDefinitionState{}, fmt.Errorf("decode shared Workflow definition: %w", err)
	}
	if state.ContractVersion != workflowDefinitionContractVersion || strings.TrimSpace(state.Definition.ID) == "" || strings.TrimSpace(state.Definition.Key) == "" || workflowDefinitionResourceKey(state.Definition.Key) != metadata.ResourceKey || strings.TrimSpace(metadata.ObjectKey) != strings.TrimSpace(state.Definition.ID) {
		return workflowDefinitionState{}, fmt.Errorf("shared Workflow definition identity is inconsistent")
	}
	return state, nil
}

func decodeWorkflowVersion(metadata metadatasdk.Definition) (workflowVersionState, error) {
	var state workflowVersionState
	if err := json.Unmarshal(metadata.Payload, &state); err != nil {
		return workflowVersionState{}, fmt.Errorf("decode shared Workflow definition version: %w", err)
	}
	if state.ContractVersion != workflowVersionContractVersion || strings.TrimSpace(state.Version.ID) == "" || strings.TrimSpace(state.Version.DefinitionID) == "" || workflowVersionResourceKey(state.Version.ID) != metadata.ResourceKey || strings.TrimSpace(metadata.ObjectKey) != strings.TrimSpace(state.Version.ID) {
		return workflowVersionState{}, fmt.Errorf("shared Workflow definition version identity is inconsistent")
	}
	state.Version.PublishIdempotencyKey = state.PublishIdempotencyKey
	return state, nil
}

func (r WorkflowDefinitionStore) publishDefinition(ctx context.Context, expectedVersionID string, state workflowDefinitionState) (metadatasdk.DefinitionPublishResult, error) {
	state.ContractVersion = workflowDefinitionContractVersion
	payload, hash, err := workflowDefinitionPayload(state)
	if err != nil {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	return r.shared.Publish(ctx, metadatasdk.DefinitionPublishCommand{
		Owner: metadatasdk.DefinitionOwnerWorkflow, ResourceType: workflowDefinitionKind,
		ResourceKey: workflowDefinitionResourceKey(state.Definition.Key), ExpectedCurrentVersionID: expectedVersionID,
		SchemaVersion: workflowDefinitionContractVersion + ":" + hash, SchemaHash: hash,
		ObjectKey: state.Definition.ID, Name: state.Definition.Name, Payload: payload,
		SourceKind: workflowDefinitionSourceKind, SourceID: state.Definition.ID, PublishedBy: workflowDefinitionPublishedBy(state.Definition, nil),
	})
}

func (r WorkflowDefinitionStore) publishVersion(ctx context.Context, expectedVersionID string, state workflowVersionState) (metadatasdk.DefinitionPublishResult, error) {
	state.ContractVersion = workflowVersionContractVersion
	state.PublishIdempotencyKey = state.Version.PublishIdempotencyKey
	payload, hash, err := workflowDefinitionPayload(state)
	if err != nil {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	return r.shared.Publish(ctx, metadatasdk.DefinitionPublishCommand{
		Owner: metadatasdk.DefinitionOwnerWorkflow, ResourceType: workflowDefinitionKind,
		ResourceKey: workflowVersionResourceKey(state.Version.ID), ExpectedCurrentVersionID: expectedVersionID,
		SchemaVersion: workflowVersionContractVersion + ":" + hash, SchemaHash: hash,
		ObjectKey: state.Version.ID, Name: state.Version.Workflow.Name, Payload: payload,
		SourceKind: workflowDefinitionSourceKind, SourceID: state.Version.DefinitionID, PublishedBy: workflowDefinitionPublishedBy(workflowmodel.WorkflowDefinition{}, &state.Version),
	})
}

func workflowDefinitionPayload(value any) ([]byte, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func workflowDefinitionResourceKey(key string) string { return strings.TrimSpace(key) }

func workflowVersionResourceKey(id string) string {
	return workflowVersionResourcePrefix + strings.TrimSpace(id)
}

func workflowDefinitionPublishedBy(definition workflowmodel.WorkflowDefinition, version *workflowmodel.WorkflowDefinitionVersion) string {
	if version != nil {
		for _, actor := range []string{version.PublishedBy, version.CreatedBy} {
			if strings.TrimSpace(actor) != "" {
				return strings.TrimSpace(actor)
			}
		}
	}
	if strings.TrimSpace(definition.OwnerUserID) != "" {
		return strings.TrimSpace(definition.OwnerUserID)
	}
	return "system:workflow"
}

func workflowSchemasEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (r WorkflowDefinitionStore) mutate(ctx context.Context, change func(context.Context) (bool, error)) (bool, error) {
	if err := r.validate(); err != nil {
		return false, err
	}
	var lastErr error
	for attempt := 0; attempt < workflowDefinitionMutationRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		tx, err := r.store.DB().BeginTx(ctx, recordMutationTxOptions())
		if err != nil {
			if !workflowDefinitionRetryable(err) {
				return false, err
			}
			lastErr = err
			if err := waitWorkflowDefinitionRetry(ctx, attempt); err != nil {
				return false, err
			}
			continue
		}
		sharedCtx := metadatamodulehost.WithExecutor(ctx, tx)
		changed, changeErr := change(sharedCtx)
		if changeErr == nil && changed {
			changeErr = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if changeErr == nil {
			return changed, nil
		}
		_ = tx.Rollback()
		if !workflowDefinitionRetryable(changeErr) {
			return false, changeErr
		}
		lastErr = changeErr
		if err := waitWorkflowDefinitionRetry(ctx, attempt); err != nil {
			return false, err
		}
	}
	return false, lastErr
}

func workflowDefinitionRetryable(err error) bool {
	var metadataErr *metadatasdk.Error
	if errors.As(err, &metadataErr) && metadataErr.StatusCode == 409 {
		return true
	}
	return mutation.IsTransactionTransient(mutation.TransactionError(err, "workflow_definition", "publication"), "")
}

func waitWorkflowDefinitionRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt+1) * time.Millisecond
	if delay > 10*time.Millisecond {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
