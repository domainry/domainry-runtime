package automation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	automationRuleDefinitionKind            = "automation_rule"
	automationRuleDefinitionSourceKind      = "automation_management"
	automationRuleDefinitionContractVersion = "domainry-automation-rule-definition-v1"
)

type automationRuleDefinitionState struct {
	ContractVersion string                                   `json:"contract_version"`
	Definition      automationmodel.AutomationRuleDefinition `json:"definition"`
}

type AutomationRuleDefinitionStore struct {
	definitions metadatasdk.DefinitionStore
}

func NewAutomationRuleDefinitionStore(store *database.RuntimeStore) AutomationRuleDefinitionStore {
	var definitions metadatasdk.DefinitionStore
	if store != nil && store.Metadata() != nil {
		definitions = store.Metadata().DefinitionStore()
	}
	return AutomationRuleDefinitionStore{definitions: definitions}
}

func (s AutomationRuleDefinitionStore) validate() error {
	if s.definitions == nil {
		return fmt.Errorf("Automation shared Definition store is unavailable")
	}
	return nil
}

func (s AutomationRuleDefinitionStore) Get(ctx context.Context, workspaceID, ruleKey string) (automationmodel.AutomationRuleDefinition, bool, error) {
	if err := s.validate(); err != nil {
		return automationmodel.AutomationRuleDefinition{}, false, err
	}
	metadata, found, err := s.definitions.Get(ctx, metadatasdk.DefinitionOwnerAutomation, automationRuleDefinitionKind, automationRuleDefinitionResourceKey(workspaceID, ruleKey))
	if err != nil || !found {
		return automationmodel.AutomationRuleDefinition{}, found, err
	}
	definition, err := decodeAutomationRuleDefinition(metadata, workspaceID, ruleKey)
	return definition, err == nil, err
}

func (s AutomationRuleDefinitionStore) List(ctx context.Context, workspaceID string) ([]automationmodel.AutomationRuleDefinition, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("Automation workspace is required")
	}
	items, err := s.definitions.List(ctx, metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerAutomation, ResourceType: automationRuleDefinitionKind, SourceID: workspaceID})
	if err != nil {
		return nil, err
	}
	result := make([]automationmodel.AutomationRuleDefinition, 0, len(items))
	for _, item := range items {
		definition, err := decodeAutomationRuleDefinition(item, workspaceID, "")
		if err != nil {
			return nil, err
		}
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RuleKey < result[j].RuleKey })
	return result, nil
}

func (s AutomationRuleDefinitionStore) Put(ctx context.Context, next automationmodel.AutomationRuleDefinition, expectedRevision int) (automationmodel.AutomationRuleDefinition, bool, error) {
	if err := s.validate(); err != nil {
		return automationmodel.AutomationRuleDefinition{}, false, err
	}
	workspaceID, ruleKey := strings.TrimSpace(next.WorkspaceID), strings.TrimSpace(next.RuleKey)
	if workspaceID == "" || ruleKey == "" || expectedRevision < 0 {
		return automationmodel.AutomationRuleDefinition{}, false, fmt.Errorf("Automation workspace, rule key and non-negative expected revision are required")
	}
	resourceKey := automationRuleDefinitionResourceKey(workspaceID, ruleKey)
	current, found, err := s.definitions.Get(ctx, metadatasdk.DefinitionOwnerAutomation, automationRuleDefinitionKind, resourceKey)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, false, err
	}
	expectedVersionID := metadatasdk.DefinitionNoCurrentVersion
	if found {
		stored, decodeErr := decodeAutomationRuleDefinition(current, workspaceID, ruleKey)
		if decodeErr != nil {
			return automationmodel.AutomationRuleDefinition{}, false, decodeErr
		}
		if stored.Revision != expectedRevision {
			return stored, false, nil
		}
		expectedVersionID = current.CurrentVersionID
	} else if expectedRevision != 0 {
		return automationmodel.AutomationRuleDefinition{}, false, nil
	}
	next.WorkspaceID, next.RuleKey, next.Revision = workspaceID, ruleKey, expectedRevision+1
	payload, hash, err := automationRuleDefinitionPayload(next)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, false, err
	}
	_, err = s.definitions.Publish(ctx, metadatasdk.DefinitionPublishCommand{
		Owner: metadatasdk.DefinitionOwnerAutomation, ResourceType: automationRuleDefinitionKind, ResourceKey: resourceKey,
		ExpectedCurrentVersionID: expectedVersionID, SchemaVersion: automationRuleDefinitionContractVersion + ":" + hash, SchemaHash: hash,
		ObjectKey: ruleKey, Name: automationRuleDefinitionName(next), Payload: payload,
		SourceKind: automationRuleDefinitionSourceKind, SourceID: workspaceID, PublishedBy: next.UpdatedBy,
	})
	if err != nil {
		var metadataErr *metadatasdk.Error
		if errors.As(err, &metadataErr) && metadataErr.StatusCode == 409 {
			latest, _, loadErr := s.Get(ctx, workspaceID, ruleKey)
			return latest, false, loadErr
		}
		return automationmodel.AutomationRuleDefinition{}, false, err
	}
	return next, true, nil
}

func decodeAutomationRuleDefinition(metadata metadatasdk.Definition, workspaceID, ruleKey string) (automationmodel.AutomationRuleDefinition, error) {
	var state automationRuleDefinitionState
	if err := database.UnmarshalTimeJSON(metadata.Payload, &state); err != nil {
		return automationmodel.AutomationRuleDefinition{}, fmt.Errorf("decode shared Automation rule definition: %w", err)
	}
	definition := state.Definition
	if state.ContractVersion != automationRuleDefinitionContractVersion || strings.TrimSpace(definition.WorkspaceID) == "" || strings.TrimSpace(definition.RuleKey) == "" || definition.Revision <= 0 || automationRuleDefinitionResourceKey(definition.WorkspaceID, definition.RuleKey) != metadata.ResourceKey || strings.TrimSpace(metadata.SourceID) != strings.TrimSpace(definition.WorkspaceID) {
		return automationmodel.AutomationRuleDefinition{}, fmt.Errorf("shared Automation rule definition identity is inconsistent")
	}
	if strings.TrimSpace(workspaceID) != "" && strings.TrimSpace(definition.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return automationmodel.AutomationRuleDefinition{}, fmt.Errorf("shared Automation workspace is inconsistent")
	}
	if strings.TrimSpace(ruleKey) != "" && strings.TrimSpace(definition.RuleKey) != strings.TrimSpace(ruleKey) {
		return automationmodel.AutomationRuleDefinition{}, fmt.Errorf("shared Automation rule key is inconsistent")
	}
	return definition, nil
}

func automationRuleDefinitionPayload(definition automationmodel.AutomationRuleDefinition) ([]byte, string, error) {
	payload, err := database.MarshalTimeJSON(automationRuleDefinitionState{ContractVersion: automationRuleDefinitionContractVersion, Definition: definition})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func automationRuleDefinitionResourceKey(workspaceID, ruleKey string) string {
	encode := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(value)))
	}
	return "workspace:" + encode(workspaceID) + ":rule:" + encode(ruleKey)
}

func automationRuleDefinitionName(definition automationmodel.AutomationRuleDefinition) string {
	if definition.Draft != nil && strings.TrimSpace(definition.Draft.Rule.Name) != "" {
		return strings.TrimSpace(definition.Draft.Rule.Name)
	}
	if definition.Published != nil && strings.TrimSpace(definition.Published.Rule.Name) != "" {
		return strings.TrimSpace(definition.Published.Rule.Name)
	}
	return definition.RuleKey
}
