package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

const workflowProcessPageMaximum = 200

type workflowProcessCursor struct {
	Version   int    `json:"v"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

// A cursor is a position, never authority. Each page runs the same live
// participant visibility query, and a changed filter or authorization revision
// requires a fresh traversal. Immutable creation time and ID break timestamp ties.
func (s *WorkflowApplicationService) workflowProcessPage(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter, participant bool) (workflowmodel.WorkflowProcessPage, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowProcessPage{}, err
	}
	size := filter.PageSize
	if size == 0 {
		size = 100
	}
	if size < 1 || size > workflowProcessPageMaximum {
		return workflowmodel.WorkflowProcessPage{}, badRequest("backend.workflow.page_size_invalid")
	}
	scope := workflowProcessCursorScope(principal, filter, participant)
	if raw := strings.TrimSpace(filter.Cursor); raw != "" {
		var cursor workflowProcessCursor
		if len(raw) > 4096 {
			return workflowmodel.WorkflowProcessPage{}, badRequest("backend.workflow.cursor_invalid")
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Scope != scope || strings.TrimSpace(cursor.ID) == "" {
			return workflowmodel.WorkflowProcessPage{}, badRequest("backend.workflow.cursor_invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt); err != nil {
			return workflowmodel.WorkflowProcessPage{}, badRequest("backend.workflow.cursor_invalid")
		}
		filter.AfterCreatedAt, filter.AfterID = cursor.CreatedAt, cursor.ID
	} else {
		filter.AfterCreatedAt, filter.AfterID = "", ""
	}
	filter.Cursor, filter.PageSize, filter.Limit = "", 0, size+1
	items, err := s.workflowProcesses(ctx, principal, filter, participant, !participant)
	if err != nil {
		return workflowmodel.WorkflowProcessPage{}, err
	}
	page := workflowmodel.WorkflowProcessPage{Items: items, HasMore: len(items) > size}
	if page.HasMore {
		page.Items = items[:size]
		last := page.Items[len(page.Items)-1]
		encoded, _ := json.Marshal(workflowProcessCursor{Version: 1, Scope: scope, CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

func workflowProcessCursorScope(principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter, participant bool) string {
	filter.Cursor, filter.Limit, filter.PageSize = "", 0, 0
	filter.AfterCreatedAt, filter.AfterID, filter.VisibleToUserID = "", "", ""
	filter.ProcessID, filter.WorkflowKey, filter.ObjectKey, filter.RecordID = strings.TrimSpace(filter.ProcessID), strings.TrimSpace(filter.WorkflowKey), strings.TrimSpace(filter.ObjectKey), strings.TrimSpace(filter.RecordID)
	filter.Status, filter.InitiatorID, filter.ApproverID = strings.TrimSpace(filter.Status), strings.TrimSpace(filter.InitiatorID), strings.TrimSpace(filter.ApproverID)
	filter.UpdatedFrom, filter.UpdatedTo = strings.TrimSpace(filter.UpdatedFrom), strings.TrimSpace(filter.UpdatedTo)
	filter.Statuses = normalizeWorkflowProcessStatuses(filter.Statuses)
	sort.Strings(filter.Statuses)
	data, _ := json.Marshal(struct {
		Workspace, User, Role, Revision string
		Participant                     bool
		Filter                          workflowmodel.WorkflowProcessFilter
	}{principal.WorkspaceID, principal.UserID, principal.RoleKey, principal.EffectiveAuthorizationRevision(), participant, filter})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type ParticipantWorkflowProcessPageDTO struct {
	Items      []ParticipantWorkflowProcessDTO `json:"items"`
	NextCursor string                          `json:"next_cursor,omitempty"`
	HasMore    bool                            `json:"has_more"`
}

func (s *WorkflowApplicationService) ParticipantWorkflowProcessPage(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) (ParticipantWorkflowProcessPageDTO, error) {
	page, err := s.workflowProcessPage(ctx, principal, filter, true)
	if err != nil {
		return ParticipantWorkflowProcessPageDTO{}, err
	}
	result := ParticipantWorkflowProcessPageDTO{Items: make([]ParticipantWorkflowProcessDTO, 0, len(page.Items)), NextCursor: page.NextCursor, HasMore: page.HasMore}
	for _, process := range page.Items {
		result.Items = append(result.Items, ProjectParticipantWorkflowProcess(process))
	}
	return result, nil
}

type OpsWorkflowProcessPageDTO struct {
	Items      []OpsWorkflowProcessDTO `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

func (s *WorkflowApplicationService) OpsWorkflowProcessPage(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) (OpsWorkflowProcessPageDTO, error) {
	page, err := s.workflowProcessPage(ctx, principal, filter, false)
	if err != nil {
		return OpsWorkflowProcessPageDTO{}, err
	}
	result := OpsWorkflowProcessPageDTO{Items: make([]OpsWorkflowProcessDTO, 0, len(page.Items)), NextCursor: page.NextCursor, HasMore: page.HasMore}
	for _, process := range page.Items {
		result.Items = append(result.Items, ProjectOpsWorkflowProcess(process))
	}
	return result, nil
}
