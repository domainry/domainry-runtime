package transport

import (
	"context"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	knowledgecontract "github.com/domainry/domainry-knowledge-sdk/contract"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentTaskKnowledgeSource interface {
	knowledgecontract.Knowledge
	SearchLibraryKnowledge(context.Context, string, string, knowledgesdk.Authority) (knowledgesdk.Result, error)
	ReadLibraryKnowledge(context.Context, string, string, knowledgesdk.Authority) (knowledgesdk.Result, error)
}

func TaskKnowledgeSource(source any) AgentTaskKnowledgeSource {
	value, _ := source.(AgentTaskKnowledgeSource)
	return value
}

type agentTaskKnowledgeAdapter struct {
	source    AgentTaskKnowledgeSource
	runtimeID string
}

// Raw provider Data and configuration never enter this projection.
type agentTaskKnowledgeResult struct {
	LibraryID   string                  `json:"library_id"`
	Operation   string                  `json:"operation"`
	Query       string                  `json:"query,omitempty"`
	DocumentID  string                  `json:"document_id,omitempty"`
	ScopeSHA256 string                  `json:"scope_sha256"`
	Citations   []knowledgesdk.Citation `json:"citations"`
}

func (a agentTaskKnowledgeAdapter) SearchAgentKnowledge(ctx context.Context, libraryID, query string, p principalmodel.Principal) (any, error) {
	return a.retrieve(ctx, "search", libraryID, query, "", p)
}
func (a agentTaskKnowledgeAdapter) ReadAgentKnowledge(ctx context.Context, libraryID, documentID string, p principalmodel.Principal) (any, error) {
	return a.retrieve(ctx, "fetch", libraryID, "", documentID, p)
}
func (a agentTaskKnowledgeAdapter) retrieve(ctx context.Context, operation, libraryID, query, documentID string, p principalmodel.Principal) (any, error) {
	if a.source == nil || strings.TrimSpace(a.runtimeID) == "" {
		return nil, apperror.New(apperror.KindUnavailable, "agent.tool.knowledge_unavailable", nil, nil)
	}
	if !p.Known || p.WorkspaceID == "" || p.UserID == "" || p.RoleKey == "" {
		return nil, apperror.New(apperror.KindForbidden, "agent.tool.knowledge_identity_denied", nil, nil)
	}
	authority := knowledgesdk.Authority{Known: true, RuntimeID: a.runtimeID, WorkspaceID: p.WorkspaceID, UserID: p.UserID, RoleKey: p.RoleKey}
	var result knowledgesdk.Result
	var err error
	if operation == "search" {
		result, err = a.source.SearchLibraryKnowledge(ctx, libraryID, query, authority)
	} else {
		result, err = a.source.ReadLibraryKnowledge(ctx, libraryID, documentID, authority)
	}
	if err != nil {
		return nil, taskKnowledgeError(err)
	}
	if result.LibraryID != libraryID || result.Operation != operation || result.Query != query || result.DocumentID != documentID || result.ScopeSHA256 == "" || len(result.Citations) > 20 {
		return nil, apperror.New(apperror.KindUnavailable, "agent.tool.knowledge_result_invalid", nil, nil)
	}
	if err = a.source.RevalidateKnowledge(ctx, result, authority); err != nil {
		return nil, taskKnowledgeError(err)
	}
	out := agentTaskKnowledgeResult{LibraryID: libraryID, Operation: operation, Query: query, DocumentID: documentID, ScopeSHA256: result.ScopeSHA256, Citations: make([]knowledgesdk.Citation, 0, len(result.Citations))}
	for _, citation := range result.Citations {
		if citation.LibraryID != libraryID || citation.ID == "" || citation.DocumentID == "" || citation.Operation != operation || documentID != "" && citation.DocumentID != documentID {
			return nil, apperror.New(apperror.KindUnavailable, "agent.tool.knowledge_result_invalid", nil, nil)
		}
		out.Citations = append(out.Citations, citation)
	}
	return out, nil
}

func taskKnowledgeError(err error) error {
	kind, code := apperror.KindUnavailable, "agent.tool.knowledge_failed"
	var typed *knowledgesdk.Error
	if errors.As(err, &typed) {
		switch typed.Class {
		case "forbidden", "not_found":
			kind, code = apperror.KindForbidden, "agent.tool.knowledge_denied"
		case "bad_request":
			kind, code = apperror.KindBadRequest, "agent.tool.knowledge_input_invalid"
		case "conflict":
			kind, code = apperror.KindConflict, "agent.tool.knowledge_conflict"
		case "rate_limited":
			kind, code = apperror.KindRateLimited, "agent.tool.knowledge_rate_limited"
		}
	}
	return apperror.New(kind, code, err, nil)
}

var _ agentapplication.AgentToolKnowledgePort = agentTaskKnowledgeAdapter{}
