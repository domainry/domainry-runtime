package codingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (r *Runtime) terminalOpen(ctx context.Context, key string, raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct{ Columns, Rows uint16 }
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	if in.Columns == 0 {
		in.Columns = 120
	}
	if in.Rows == 0 {
		in.Rows = 30
	}
	cmd, err := r.sandboxCommand(context.WithoutCancel(ctx), "", []string{r.shell})
	if err != nil {
		return failed("coding_terminal_unavailable")
	}
	r.mu.Lock()
	state := r.scopes[key]
	if state != nil && len(state.terminals) >= 8 {
		r.mu.Unlock()
		return failed("coding_terminal_limit")
	}
	r.mu.Unlock()
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: in.Columns, Rows: in.Rows})
	if err != nil {
		return failed("coding_terminal_unavailable")
	}
	id := resourceID("term", key, "")
	p := &processState{id: id, cmd: cmd, in: file, out: &boundedBuffer{}, done: make(chan struct{})}
	r.mu.Lock()
	state = r.scopes[key]
	if state == nil {
		state = &scopeState{terminals: map[string]*processState{}, processes: map[string]*processState{}}
		r.scopes[key] = state
	}
	state.terminals[id] = p
	r.mu.Unlock()
	go p.capture(file)
	return completed(map[string]any{"kind": "terminal", "terminal_id": id, "status": "running", "cursor": 0, "columns": in.Columns, "rows": in.Rows})
}

func (p *processState) capture(reader io.ReadCloser) {
	_, _ = io.Copy(p.out, reader)
	_ = reader.Close()
	err := p.cmd.Wait()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
		}
	}
	p.mu.Lock()
	p.exitCode = &code
	p.mu.Unlock()
	close(p.done)
}

func (r *Runtime) terminalSend(key string, raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct {
		ID    string `json:"terminal_id"`
		Input string `json:"input"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	p := r.lookup(key, in.ID, true)
	if p == nil {
		return failed("coding_terminal_unavailable")
	}
	select {
	case <-p.done:
		return failed("coding_terminal_closed")
	default:
	}
	if _, err := io.WriteString(p.in, in.Input); err != nil {
		return failed("coding_terminal_write_failed")
	}
	return completed(map[string]any{"kind": "terminal_input", "terminal_id": in.ID, "bytes": len(in.Input)})
}

func (r *Runtime) processStart(ctx context.Context, key string, raw json.RawMessage) agentsdk.ConversationToolResult {
	var in struct {
		Argv      []string `json:"argv"`
		Directory string   `json:"working_directory"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Argv) == 0 {
		return failed("coding_input_invalid")
	}
	cmd, err := r.sandboxCommand(context.WithoutCancel(ctx), in.Directory, in.Argv)
	if err != nil {
		return failed("coding_process_unavailable")
	}
	r.mu.Lock()
	state := r.scopes[key]
	if state != nil && len(state.processes) >= 32 {
		r.mu.Unlock()
		return failed("coding_process_limit")
	}
	r.mu.Unlock()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failed("coding_process_unavailable")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return failed("coding_process_unavailable")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return failed("coding_process_unavailable")
	}
	if err = cmd.Start(); err != nil {
		return failed("coding_process_unavailable")
	}
	id := resourceID("proc", key, "")
	p := &processState{id: id, cmd: cmd, in: stdin, out: &boundedBuffer{}, done: make(chan struct{})}
	r.mu.Lock()
	state = r.scopes[key]
	if state == nil {
		state = &scopeState{terminals: map[string]*processState{}, processes: map[string]*processState{}}
		r.scopes[key] = state
	}
	state.processes[id] = p
	r.mu.Unlock()
	go func() {
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); _, _ = io.Copy(p.out, stdout) }()
		go func() { defer wait.Done(); _, _ = io.Copy(p.out, stderr) }()
		err := cmd.Wait()
		wait.Wait()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		p.mu.Lock()
		p.exitCode = &code
		p.mu.Unlock()
		close(p.done)
	}()
	return completed(map[string]any{"kind": "process", "process_id": id, "status": "running", "cursor": 0, "argv": in.Argv, "working_directory": in.Directory})
}

func (r *Runtime) lookup(key, id string, terminal bool) *processState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.scopes[key]
	if state == nil {
		return nil
	}
	if terminal {
		return state.terminals[id]
	}
	return state.processes[id]
}

func (r *Runtime) processRead(key string, raw json.RawMessage, terminal bool) agentsdk.ConversationToolResult {
	var in struct {
		TerminalID string `json:"terminal_id"`
		ProcessID  string `json:"process_id"`
		Cursor     int64  `json:"cursor"`
		MaxBytes   int    `json:"max_bytes"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = 32768
	}
	id := in.ProcessID
	if terminal {
		id = in.TerminalID
	}
	p := r.lookup(key, id, terminal)
	if p == nil {
		return failed(map[bool]string{true: "coding_terminal_unavailable", false: "coding_process_unavailable"}[terminal])
	}
	output, next, more, err := p.out.read(in.Cursor, in.MaxBytes)
	if err != nil {
		return failed(err.Error())
	}
	status := "running"
	var exitCode *int
	p.mu.Lock()
	if p.exitCode != nil {
		status = "exited"
		value := *p.exitCode
		exitCode = &value
	}
	p.mu.Unlock()
	kind := "process_output"
	idKey := "process_id"
	if terminal {
		kind = "terminal_output"
		idKey = "terminal_id"
	}
	return completed(map[string]any{"kind": kind, idKey: id, "status": status, "output": output, "cursor": in.Cursor, "next_cursor": next, "more": more, "exit_code": exitCode})
}

func (r *Runtime) processStop(key string, raw json.RawMessage, terminal bool) agentsdk.ConversationToolResult {
	var in struct {
		TerminalID string `json:"terminal_id"`
		ProcessID  string `json:"process_id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return failed("coding_input_invalid")
	}
	id := in.ProcessID
	if terminal {
		id = in.TerminalID
	}
	p := r.lookup(key, id, terminal)
	if p == nil {
		return failed(map[bool]string{true: "coding_terminal_unavailable", false: "coding_process_unavailable"}[terminal])
	}
	if p.cmd != nil && p.cmd.Process != nil {
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM); err != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
	return completed(map[string]any{"kind": map[bool]string{true: "terminal", false: "process"}[terminal], map[bool]string{true: "terminal_id", false: "process_id"}[terminal]: id, "status": "stopping"})
}
