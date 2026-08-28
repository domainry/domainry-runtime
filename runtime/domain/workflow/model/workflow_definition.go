package workflowmodel

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

const (
	WorkflowVersionDraft     = "draft"
	WorkflowVersionPublished = "published"
	WorkflowVersionArchived  = "archived"
)

type WorkflowDefinition struct {
	ID                        string `json:"id"`
	Key                       string `json:"key"`
	Name                      string `json:"name"`
	OwnerUserID               string `json:"owner_user_id,omitempty"`
	Enabled                   bool   `json:"enabled"`
	CurrentDraftVersionID     string `json:"current_draft_version_id,omitempty"`
	CurrentPublishedVersionID string `json:"current_published_version_id,omitempty"`
	CreatedAt                 string `json:"created_at"`
	UpdatedAt                 string `json:"updated_at"`
	CurrentDraftVersion       int    `json:"current_draft_version,omitempty"`
	CurrentDraftStatus        string `json:"current_draft_status,omitempty"`
	CurrentPublishedVersion   int    `json:"current_published_version,omitempty"`
	LastPublishedAt           string `json:"last_published_at,omitempty"`
	LastPublishedBy           string `json:"last_published_by,omitempty"`
}

type WorkflowDefinitionVersion struct {
	ID                    string                         `json:"id"`
	DefinitionID          string                         `json:"definition_id"`
	Version               int                            `json:"version"`
	Status                string                         `json:"status"`
	Revision              int                            `json:"revision"`
	ContentHash           string                         `json:"content_hash,omitempty"`
	Workflow              definitionmodel.WorkflowSchema `json:"workflow"`
	ValidationReport      WorkflowValidation             `json:"validation_report"`
	PublishNote           string                         `json:"publish_note,omitempty"`
	PublishIdempotencyKey string                         `json:"-"`
	CreatedBy             string                         `json:"created_by"`
	PublishedBy           string                         `json:"published_by,omitempty"`
	CreatedAt             string                         `json:"created_at"`
	UpdatedAt             string                         `json:"updated_at"`
	PublishedAt           string                         `json:"published_at,omitempty"`
	ArchivedAt            string                         `json:"archived_at,omitempty"`
}

type WorkflowValidation struct {
	Valid       bool                      `json:"valid"`
	Issues      []WorkflowValidationIssue `json:"issues"`
	ValidatedAt string                    `json:"validated_at"`
}

type WorkflowValidationIssue struct {
	Severity        string            `json:"severity"`
	Code            string            `json:"code"`
	MessageKey      string            `json:"message_key"`
	Message         string            `json:"message"`
	FieldPath       string            `json:"field_path"`
	NodeID          string            `json:"node_id,omitempty"`
	EdgeID          string            `json:"edge_id,omitempty"`
	Params          map[string]string `json:"params,omitempty"`
	CapabilityKey   string            `json:"capability_key"`
	ContractVersion string            `json:"contract_version"`
	Diagnostic      string            `json:"diagnostic,omitempty"`
}
