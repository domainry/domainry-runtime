// Package codingruntime provides the deployment-owned, restricted execution
// world used by Agent coding tools. It is not a business-tool registry.
package codingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type LanguageServer struct {
	Extensions []string
	LanguageID string
	Command    []string
}

type Options struct {
	WorkspaceRoot   string
	Shell           string
	LanguageServers []LanguageServer
}

type Runtime struct {
	rootPath string
	root     *os.Root
	shell    string
	lsp      map[string]LanguageServer
	fileMu   sync.Mutex
	mu       sync.Mutex
	scopes   map[string]*scopeState
	receipts map[string]agentsdk.ConversationToolResult
}

type scopeState struct {
	terminals map[string]*processState
	processes map[string]*processState
}

type processState struct {
	id       string
	cmd      *exec.Cmd
	in       io.WriteCloser
	out      *boundedBuffer
	done     chan struct{}
	mu       sync.Mutex
	exitCode *int
}

type boundedBuffer struct {
	mu   sync.Mutex
	base int64
	data []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.data = append(b.data, p...)
	const limit = 2 << 20
	if len(b.data) > limit {
		drop := len(b.data) - limit
		b.data = append([]byte(nil), b.data[drop:]...)
		b.base += int64(drop)
	}
	return n, nil
}

func (b *boundedBuffer) read(cursor int64, maxBytes int) (string, int64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cursor < b.base {
		return "", b.base, false, errors.New("cursor_expired")
	}
	endCursor := b.base + int64(len(b.data))
	if cursor > endCursor {
		return "", endCursor, false, errors.New("cursor_invalid")
	}
	start := int(cursor - b.base)
	end := min(len(b.data), start+maxBytes)
	return string(b.data[start:end]), b.base + int64(end), end < len(b.data), nil
}

func New(options Options) (*Runtime, error) {
	path, err := filepath.Abs(strings.TrimSpace(options.WorkspaceRoot))
	if err != nil || path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return nil, fmt.Errorf("coding workspace root is required")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("coding workspace root must be an existing directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	shell := strings.TrimSpace(options.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	if !filepath.IsAbs(shell) {
		root.Close()
		return nil, fmt.Errorf("coding shell must be absolute")
	}
	r := &Runtime{rootPath: path, root: root, shell: shell, lsp: map[string]LanguageServer{}, scopes: map[string]*scopeState{}, receipts: map[string]agentsdk.ConversationToolResult{}}
	for _, server := range options.LanguageServers {
		if strings.TrimSpace(server.LanguageID) == "" || len(server.Command) == 0 || !filepath.IsAbs(server.Command[0]) {
			root.Close()
			return nil, fmt.Errorf("invalid coding language server")
		}
		for _, ext := range server.Extensions {
			ext = strings.ToLower(strings.TrimSpace(ext))
			if !strings.HasPrefix(ext, ".") || ext == "." {
				root.Close()
				return nil, fmt.Errorf("invalid coding language extension")
			}
			if _, exists := r.lsp[ext]; exists {
				root.Close()
				return nil, fmt.Errorf("duplicate coding language extension")
			}
			r.lsp[ext] = server
		}
	}
	if runtime.GOOS != "darwin" {
		root.Close()
		return nil, fmt.Errorf("coding runtime requires a configured OS sandbox; this build supports darwin sandbox-exec")
	}
	if _, err = os.Stat("/usr/bin/sandbox-exec"); err != nil {
		root.Close()
		return nil, fmt.Errorf("coding runtime sandbox is unavailable")
	}
	return r, nil
}

func scopeKey(s agentsdk.ConversationCodingScope) (string, error) {
	parts := []string{s.RuntimeID, s.WorkspaceID, s.UserID, s.ConversationID, s.RunID}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" || strings.ContainsAny(p, "\x00\r\n") {
			return "", errors.New("coding_scope_invalid")
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), nil
}

func completed(value any) agentsdk.ConversationToolResult {
	raw, _ := json.Marshal(value)
	return agentsdk.ConversationToolResult{Status: "completed", Content: raw}
}
func failed(code string) agentsdk.ConversationToolResult {
	raw, _ := json.Marshal(map[string]any{"error": code})
	return agentsdk.ConversationToolResult{Status: "failed", ErrorCode: code, Content: raw}
}

func (r *Runtime) ExecuteConversationCoding(ctx context.Context, request agentsdk.ConversationCodingRequest) (agentsdk.ConversationToolResult, error) {
	key, err := scopeKey(request.Scope)
	if err != nil {
		return failed("coding_scope_invalid"), nil
	}
	if !agentsdk.IsConversationCodingTool(request.Tool) || !json.Valid(request.Arguments) {
		return failed("coding_input_invalid"), nil
	}
	if request.IdempotencyKey != "" {
		r.mu.Lock()
		prior, ok := r.receipts[key+"\x00"+request.IdempotencyKey]
		r.mu.Unlock()
		if ok {
			return prior, nil
		}
		if request.Reconcile && request.Tool != "coding_file_write" && request.Tool != "coding_file_edit" {
			return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "coding_receipt_unavailable", Content: json.RawMessage(`{"error":"coding_receipt_unavailable"}`)}, nil
		}
	}
	var result agentsdk.ConversationToolResult
	switch request.Tool {
	case "coding_file_read":
		result = r.fileRead(request.Arguments)
	case "coding_file_search":
		result = r.fileSearch(ctx, request.Arguments)
	case "coding_file_write":
		result = r.fileWrite(request.Arguments, false, request.Reconcile)
	case "coding_file_edit":
		result = r.fileWrite(request.Arguments, true, request.Reconcile)
	case "coding_terminal_open":
		result = r.terminalOpen(ctx, key, request.Arguments)
	case "coding_terminal_send":
		result = r.terminalSend(key, request.Arguments)
	case "coding_terminal_read":
		result = r.processRead(key, request.Arguments, true)
	case "coding_terminal_close":
		result = r.processStop(key, request.Arguments, true)
	case "coding_process_start":
		result = r.processStart(ctx, key, request.Arguments)
	case "coding_process_read":
		result = r.processRead(key, request.Arguments, false)
	case "coding_process_kill":
		result = r.processStop(key, request.Arguments, false)
	case "coding_lsp":
		result = r.lspQuery(ctx, request.Arguments)
	default:
		result = failed("coding_tool_unavailable")
	}
	if request.IdempotencyKey != "" && result.Status != "uncertain" {
		r.mu.Lock()
		r.receipts[key+"\x00"+request.IdempotencyKey] = result
		r.mu.Unlock()
	}
	return result, nil
}

func (r *Runtime) CloseConversationCodingScope(_ context.Context, scope agentsdk.ConversationCodingScope) error {
	key, err := scopeKey(scope)
	if err != nil {
		return err
	}
	r.mu.Lock()
	state := r.scopes[key]
	delete(r.scopes, key)
	for receipt := range r.receipts {
		if strings.HasPrefix(receipt, key+"\x00") {
			delete(r.receipts, receipt)
		}
	}
	r.mu.Unlock()
	if state == nil {
		return nil
	}
	for _, p := range state.terminals {
		stopProcess(p)
	}
	for _, p := range state.processes {
		stopProcess(p)
	}
	return nil
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	scopes := r.scopes
	r.scopes = map[string]*scopeState{}
	r.mu.Unlock()
	for _, state := range scopes {
		for _, p := range state.terminals {
			stopProcess(p)
		}
		for _, p := range state.processes {
			stopProcess(p)
		}
	}
	return r.root.Close()
}

func cleanRelative(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", errors.New("coding_path_invalid")
	}
	return filepath.ToSlash(path), nil
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func regularUTF8(data []byte, info os.FileInfo) bool {
	return info.Mode().IsRegular() && utf8.Valid(data)
}
func (r *Runtime) state(key string) *scopeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.scopes[key]
	if s == nil {
		s = &scopeState{terminals: map[string]*processState{}, processes: map[string]*processState{}}
		r.scopes[key] = s
	}
	return s
}
func resourceID(prefix, key, idempotency string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + key + "\x00" + idempotency + "\x00" + time.Now().UTC().Format(time.RFC3339Nano)))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func (r *Runtime) sandboxCommand(ctx context.Context, directory string, argv []string) (*exec.Cmd, error) {
	rel, err := cleanRelative(directory)
	if directory == "" {
		rel = "."
		err = nil
	}
	if err != nil {
		return nil, err
	}
	cwd := r.rootPath
	if rel != "." {
		cwd = filepath.Join(r.rootPath, filepath.FromSlash(rel))
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, errors.New("coding_working_directory_invalid")
	}
	escaped := strings.ReplaceAll(r.rootPath, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	profile := `(version 1)(allow default)(deny network*)(deny file-write*)(allow file-write* (subpath "` + escaped + `"))`
	args := append([]string{"-p", profile, "--"}, argv...)
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", args...)
	cmd.Dir = cwd
	tmp := filepath.Join(r.rootPath, ".domainry-tmp")
	_ = os.MkdirAll(tmp, 0700)
	cmd.Env = []string{"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + r.rootPath, "TMPDIR=" + tmp, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "TERM=xterm-256color"}
	return cmd, nil
}

func stopProcess(p *processState) {
	if p == nil {
		return
	}
	if p.in != nil {
		_ = p.in.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-p.done:
		case <-time.After(500 * time.Millisecond):
			if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
				_ = p.cmd.Process.Kill()
			}
		}
	}
}

var _ agentsdk.ConversationCodingRuntime = (*Runtime)(nil)
var _ io.Writer = (*boundedBuffer)(nil)
