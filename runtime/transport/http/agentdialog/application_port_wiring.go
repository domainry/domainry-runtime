package agentdialog

import (
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
)

func (h *AgentDialogHandler) UseStateApplications(sessions, proposals, governance *agentapplication.AgentApplicationService) {
	if sessions != nil {
		h.sessions = sessions
	}
	if proposals != nil {
		h.proposalState = proposals
	}
	if governance != nil {
		h.reportGovernance = governance
	}
}

func (h *AgentDialogHandler) UseProposalDecisions(decisions *agentapplication.AgentProposalApplicationService) {
	if decisions != nil {
		h.proposalDecisions = decisions
	}
}

func (h *AgentDialogHandler) UseAnalysisRecords(records *recordapplication.RecordApplicationService) {
	if records != nil {
		h.analysisRecords = records
	}
}
