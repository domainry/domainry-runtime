package workflow

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
)

// ValidateAuthoringFragment applies Workflow-owned semantic validation to one
// leaf capability payload. It is read-only and never creates a draft.
func (s *WorkflowApplicationService) ValidateAuthoringFragment(ctx context.Context, capabilityKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowValidation, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowValidation{}, err
	}
	report := emptyWorkflowValidation()
	report.CapabilityKey = capabilityKey
	report.ValidatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	normalized, err := workflowpolicy.WorkflowNormalizeAuthoringFragment(capabilityKey, payload)
	report.Fragment = normalized
	if err != nil {
		code := valueOrDefault(apperror.CodeOf(err), "backend.workflow.authoring_fragment_invalid")
		issue := workflowvalidation.WorkflowValidationIssueFromError(err, code, err.Error())
		issue.CapabilityKey = capabilityKey
		report.Issues = append(report.Issues, issue)
	}
	report.Valid = len(report.Issues) == 0
	return report, nil
}
