package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentPersistenceRemainsClassifiedByResponsibility(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve agent persistence source")
	}
	root := filepath.Dir(source)
	required := map[string]string{
		"agent_task_run_store.go":        "func (s *AgentTaskRunStore) Create",
		"agent_task_claim_store.go":      "func (s *AgentTaskRunStore) ClaimNext",
		"agent_task_transition_store.go": "func (s *AgentTaskRunStore) SaveRunning",
		"agent_tool_call_store.go":       "func (s *AgentTaskRunStore) BeginAgentToolCall",
	}
	for name, symbol := range required {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(text, symbol) {
			t.Errorf("agent persistence file %s lost responsibility %q", name, symbol)
		}
		if lines := strings.Count(text, "\n") + 1; lines > 400 {
			t.Errorf("agent persistence file %s grew into a catch-all: %d lines", name, lines)
		}
	}
	toolSource, err := os.ReadFile(filepath.Join(root, "agent_tool_call_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(toolSource), "AgentToolCallLedger") {
		t.Error("agent tool-call ledger interface assertion left its classified owner")
	}
}
