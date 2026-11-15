package runtimeext

import "testing"

func TestWorkflowAgentEvidenceIsExplicitAndUnavailableWithoutHost(t *testing.T) {
	grant := WorkflowGrant{Key: "case.decide", Operations: []string{WorkflowReadAgentTaskEvidenceOperation}}
	if !grant.Valid() {
		t.Fatal("explicit read grant rejected")
	}
	grant.Operations = append(grant.Operations, WorkflowReadAgentTaskEvidenceOperation)
	if grant.Valid() {
		t.Fatal("duplicate grant accepted")
	}
	if _, err := ReadWorkflowAgentTaskEvidence(t.Context(), nil, WorkflowAgentTaskEvidenceRequest{}); err == nil {
		t.Fatal("missing host fabricated task evidence")
	}
}
