package codingruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

var fakeLSP = flag.Bool("fake-lsp", false, "run the coding runtime fake language server")

func TestFakeLSP(t *testing.T) {
	if !*fakeLSP {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		message, err := readLSP(reader)
		if err != nil {
			return
		}
		switch message.Method {
		case "initialize":
			_ = writeLSP(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": map[string]any{"capabilities": map[string]any{"positionEncoding": "utf-16", "definitionProvider": true, "referencesProvider": true}}})
		case "textDocument/definition", "textDocument/references":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			location := map[string]any{"uri": params.TextDocument.URI, "range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 4}}}
			_ = writeLSP(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": []any{location}})
		}
	}
}

func codingRequest(scope agentsdk.ConversationCodingScope, tool string, arguments any, key string) agentsdk.ConversationCodingRequest {
	raw, _ := json.Marshal(arguments)
	return agentsdk.ConversationCodingRequest{Scope: scope, Tool: tool, Arguments: raw, IdempotencyKey: key}
}

func resultObject(t *testing.T, result agentsdk.ConversationToolResult) map[string]any {
	t.Helper()
	if result.Status != "completed" {
		t.Fatalf("result = %+v", result)
	}
	var value map[string]any
	if err := json.Unmarshal(result.Content, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRestrictedCodingWorkspace(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("darwin sandbox-exec is unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc target() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(Options{WorkspaceRoot: root, LanguageServers: []LanguageServer{{Extensions: []string{".go"}, LanguageID: "go", Command: []string{executable, "-test.run=TestFakeLSP", "-fake-lsp=true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope := agentsdk.ConversationCodingScope{RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "conversation", RunID: "run"}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	read, err := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_file_read", map[string]any{"path": "main.go"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	version := resultObject(t, read)["sha256"].(string)
	stale, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_file_edit", map[string]any{"path": "main.go", "old_text": "target", "new_text": "renamed", "expected_sha256": strings.Repeat("0", 64)}, "edit-stale"))
	if stale.ErrorCode != "coding_version_conflict" {
		t.Fatalf("stale edit = %+v", stale)
	}
	edited, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_file_edit", map[string]any{"path": "main.go", "old_text": "target", "new_text": "renamed", "expected_sha256": version}, "edit"))
	if resultObject(t, edited)["operation"] != "edit" {
		t.Fatal("edit result missing")
	}
	traversal, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_file_read", map[string]any{"path": "../secret"}, ""))
	if traversal.ErrorCode != "coding_path_invalid" {
		t.Fatalf("traversal = %+v", traversal)
	}
	search, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_file_search", map[string]any{"query": "renamed", "path": "."}, ""))
	if len(resultObject(t, search)["matches"].([]any)) != 1 {
		t.Fatal("search did not observe edit")
	}

	opened, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_terminal_open", map[string]any{}, "terminal-open"))
	terminalID := resultObject(t, opened)["terminal_id"].(string)
	_, _ = runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_terminal_send", map[string]any{"terminal_id": terminalID, "input": "printf 'pty-ok\\n'\n"}, "terminal-send"))
	if output := waitOutput(t, runtime, scope, "coding_terminal_read", "terminal_id", terminalID); !strings.Contains(output, "pty-ok") {
		t.Fatalf("PTY output = %q", output)
	}

	started, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_process_start", map[string]any{"argv": []string{"/bin/sh", "-c", "printf process-ok"}}, "process-start"))
	processID := resultObject(t, started)["process_id"].(string)
	if output := waitOutput(t, runtime, scope, "coding_process_read", "process_id", processID); !strings.Contains(output, "process-ok") {
		t.Fatalf("process output = %q", output)
	}
	outside := filepath.Join(filepath.Dir(root), "domainry-k08-outside")
	_ = os.Remove(outside)
	denied, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_process_start", map[string]any{"argv": []string{"/bin/sh", "-c", `printf denied > "$1"`, "sh", outside}}, "process-denied"))
	deniedID := resultObject(t, denied)["process_id"].(string)
	waitProcessExit(t, runtime, scope, deniedID)
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("sandbox wrote outside workspace: %v", err)
	}

	lsp, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_lsp", map[string]any{"operation": "definition", "path": "main.go", "line": 3, "character": 6}, ""))
	locations := resultObject(t, lsp)["locations"].([]any)
	if len(locations) != 1 || locations[0].(map[string]any)["path"] != "main.go" {
		t.Fatalf("LSP locations = %#v", locations)
	}
	references, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_lsp", map[string]any{"operation": "references", "path": "main.go", "line": 3, "character": 6}, ""))
	if len(resultObject(t, references)["locations"].([]any)) != 1 {
		t.Fatal("LSP references were not returned")
	}
	if err := runtime.CloseConversationCodingScope(ctx, scope); err != nil {
		t.Fatal(err)
	}
	closed, _ := runtime.ExecuteConversationCoding(ctx, codingRequest(scope, "coding_terminal_read", map[string]any{"terminal_id": terminalID}, ""))
	if closed.ErrorCode != "coding_terminal_unavailable" {
		t.Fatalf("closed scope terminal = %+v", closed)
	}
}

func waitProcessExit(t *testing.T, runtime *Runtime, scope agentsdk.ConversationCodingScope, id string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		result, _ := runtime.ExecuteConversationCoding(t.Context(), codingRequest(scope, "coding_process_read", map[string]any{"process_id": id, "cursor": 0}, ""))
		value := resultObject(t, result)
		if value["status"] == "exited" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process did not exit")
}

func waitOutput(t *testing.T, runtime *Runtime, scope agentsdk.ConversationCodingScope, tool, idKey, id string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		result, err := runtime.ExecuteConversationCoding(t.Context(), codingRequest(scope, tool, map[string]any{idKey: id, "cursor": 0}, ""))
		if err != nil {
			t.Fatal(err)
		}
		value := resultObject(t, result)
		output := fmt.Sprint(value["output"])
		if output != "" {
			return output
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}
