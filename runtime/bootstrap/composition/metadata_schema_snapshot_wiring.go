package composition

import (
	"context"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatabusiness "github.com/domainry/domainry-runtime/runtime/domain/metadata/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// RecordSchemaSnapshotProvider projects mutable Runtime schema state.
type RecordSchemaSnapshotProvider struct {
	snapshot func() metadatamodel.MetadataSchemaSnapshot
}

func ensureRecordSchemaSnapshotProvider(records *runtimeAssembly) {
	if records != nil && records.RecordSchemaSnapshotProvider == nil {
		records.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() metadatamodel.MetadataSchemaSnapshot { return recordSchemaSnapshot(records) }}
	}
}

func (s *RecordSchemaSnapshotProvider) Schema() metadatamodel.MetadataSchemaSnapshot {
	return s.snapshot()
}

func recordSchemaSnapshot(s *runtimeAssembly) metadatamodel.MetadataSchemaSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make([]definitionmodel.ObjectSchema, 0, len(s.schema))
	for _, object := range s.schema {
		objects = append(objects, object)
	}
	actions := make([]definitionmodel.ActionSchema, 0, len(s.actions))
	for _, action := range s.actions {
		actions = append(actions, action)
	}
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(s.workflows))
	for _, workflow := range s.workflows {
		workflows = append(workflows, workflow)
	}
	automationRules := make([]automationmodel.AutomationRuleSchema, 0, len(s.automationRules))
	for _, rule := range s.automationRules {
		automationRules = append(automationRules, rule)
	}
	return metadatabusiness.BuildSchemaSnapshot(metadatabusiness.SchemaSnapshotState{
		TemplateID: s.templateID, TemplateVersion: s.templateVersion, Name: s.name,
		Objects: objects, Views: s.views, Actions: actions, Workflows: workflows, AutomationRules: automationRules,
		Dictionaries: s.dictionaries, Integrations: s.integrations, Reports: s.reports, EntryPoints: s.entrypoints,
		Skills: s.skills, Agents: s.agents, AgentTasks: s.agentTasks, AgentEntrypoints: s.agentEntrypoints, AgentServicePrincipals: s.agentServicePrincipals,
		IdentityProfileExtensions: s.identityProfileExtensions,
	})
}

func (s *RecordSchemaSnapshotProvider) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	return metadatabusiness.SnapshotForPrincipal(s.Schema(), principal)
}
