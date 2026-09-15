package coderuntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == WorkerArgument {
		os.Exit(RunWorker(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func codeRequest(source string) agentsdk.ConversationCodeExecution {
	return agentsdk.ConversationCodeExecution{
		ProtocolVersion: agentsdk.ConversationCodeProtocolVersion,
		Language:        agentsdk.ConversationCodeLanguageLua,
		Source:          source,
		Tools:           []agentsdk.ConversationToolDefinition{{Key: "lookup"}},
		MaxDispatches:   4, MaxOutputBytes: 4096, MaxLogBytes: 1024,
	}
}

func TestProcessComposesToolResultWithoutGivingWorkerAToolHost(t *testing.T) {
	runtime, err := NewProcess(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := runtime.ExecuteConversationCode(t.Context(), codeRequest(`
local receipt = tools.lookup({query = "alpha"})
log({stage = "looked_up", status = receipt.status})
return {count = receipt.value.count + 1, os_available = os ~= nil, io_available = io ~= nil, require_available = require ~= nil}
`), func(_ context.Context, dispatch agentsdk.ConversationCodeDispatch) (agentsdk.ConversationToolResult, error) {
		calls++
		if dispatch.Index != 0 || dispatch.Name != "lookup" || string(dispatch.Arguments) != `{"query":"alpha"}` {
			t.Fatalf("unexpected dispatch: %+v", dispatch)
		}
		return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"count":2}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Dispatches != 1 || len(result.Logs) != 1 || string(result.Value) != `{"count":3,"io_available":false,"os_available":false,"require_available":false}` {
		t.Fatalf("unexpected result: calls=%d result=%+v", calls, result)
	}
}

func TestProcessPreservesDispatcherErrorForDurableWait(t *testing.T) {
	runtime, _ := NewProcess(os.Args[0])
	waiting := errors.New("waiting for confirmation")
	_, err := runtime.ExecuteConversationCode(t.Context(), codeRequest(`return tools.lookup({})`), func(context.Context, agentsdk.ConversationCodeDispatch) (agentsdk.ConversationToolResult, error) {
		return agentsdk.ConversationToolResult{}, waiting
	})
	if !errors.Is(err, waiting) {
		t.Fatalf("dispatcher error was replaced: %v", err)
	}
}

func TestProcessHardStopsUnboundedProgram(t *testing.T) {
	runtime, _ := NewProcess(os.Args[0])
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := runtime.ExecuteConversationCode(ctx, codeRequest(`while true do end`), func(context.Context, agentsdk.ConversationCodeDispatch) (agentsdk.ConversationToolResult, error) {
		t.Fatal("unbounded program dispatched a tool")
		return agentsdk.ConversationToolResult{}, nil
	})
	var failure *agentsdk.ConversationCodeFailure
	if !errors.As(err, &failure) || failure.Code != "code_timeout" {
		t.Fatalf("unexpected timeout: %v", err)
	}
}

func TestProcessRejectsDynamicLoadingAndMissingReturn(t *testing.T) {
	runtime, _ := NewProcess(os.Args[0])
	for _, source := range []string{`return load("return 1")`, `local value = 1`} {
		_, err := runtime.ExecuteConversationCode(t.Context(), codeRequest(source), func(context.Context, agentsdk.ConversationCodeDispatch) (agentsdk.ConversationToolResult, error) {
			return agentsdk.ConversationToolResult{}, nil
		})
		var failure *agentsdk.ConversationCodeFailure
		if !errors.As(err, &failure) || failure.Code == "" {
			t.Fatalf("unsafe or missing result was accepted: source=%q err=%v", source, err)
		}
	}
}
