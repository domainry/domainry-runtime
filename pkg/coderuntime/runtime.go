// Package coderuntime runs Agent code mode in a separate, restricted process.
// The worker has no tool host. Every tool binding returns to the parent through
// a synchronous protocol and is dispatched by Agent's normal execution path.
package coderuntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

const WorkerArgument = "__domainry-agent-code-worker-v1"

type Process struct {
	executable string
}

func NewProcess(executable string) (*Process, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return nil, fmt.Errorf("code Runtime executable is required")
	}
	return &Process{executable: executable}, nil
}

type frame struct {
	Type      string                              `json:"type"`
	Execute   *agentsdk.ConversationCodeExecution `json:"execute,omitempty"`
	Dispatch  *agentsdk.ConversationCodeDispatch  `json:"dispatch,omitempty"`
	Result    *agentsdk.ConversationToolResult    `json:"result,omitempty"`
	Complete  *agentsdk.ConversationCodeResult    `json:"complete,omitempty"`
	ErrorCode string                              `json:"error_code,omitempty"`
}

func (p *Process) ExecuteConversationCode(ctx context.Context, request agentsdk.ConversationCodeExecution, dispatch agentsdk.ConversationCodeDispatcher) (agentsdk.ConversationCodeResult, error) {
	var empty agentsdk.ConversationCodeResult
	if p == nil || p.executable == "" || dispatch == nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_runtime_unavailable"}
	}
	workdir, err := os.MkdirTemp("", "domainry-code-")
	if err != nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_runtime_unavailable"}
	}
	defer os.RemoveAll(workdir)
	command := exec.CommandContext(ctx, p.executable, WorkerArgument)
	command.Dir = workdir
	command.Env = []string{}
	stdin, err := command.StdinPipe()
	if err != nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_runtime_unavailable"}
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_runtime_unavailable"}
	}
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_runtime_unavailable"}
	}
	var closeOnce sync.Once
	stop := func() {
		closeOnce.Do(func() {
			_ = stdin.Close()
			if command.Process != nil {
				_ = command.Process.Kill()
			}
		})
	}
	defer stop()
	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(bufio.NewReaderSize(stdout, 64*1024))
	if err = encoder.Encode(frame{Type: "execute", Execute: &request}); err != nil {
		return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
	}
	for {
		var incoming frame
		if err = decoder.Decode(&incoming); err != nil {
			waitErr := command.Wait()
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return empty, &agentsdk.ConversationCodeFailure{Code: "code_timeout"}
				}
				return empty, ctx.Err()
			}
			var exit *exec.ExitError
			if errors.As(waitErr, &exit) && exit.ExitCode() == 3 {
				return empty, &agentsdk.ConversationCodeFailure{Code: "code_resource_exceeded"}
			}
			return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
		}
		switch incoming.Type {
		case "dispatch":
			if incoming.Dispatch == nil {
				return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
			}
			result, dispatchErr := dispatch(ctx, *incoming.Dispatch)
			if dispatchErr != nil {
				stop()
				_ = command.Wait()
				return empty, dispatchErr
			}
			if err = encoder.Encode(frame{Type: "result", Result: &result}); err != nil {
				return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
			}
		case "complete":
			if incoming.Complete == nil {
				return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
			}
			_ = stdin.Close()
			if err = command.Wait(); err != nil {
				return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
			}
			command.Process = nil
			return *incoming.Complete, nil
		case "error":
			_ = stdin.Close()
			_ = command.Wait()
			command.Process = nil
			code := incoming.ErrorCode
			if !strings.HasPrefix(code, "code_") {
				code = "code_runtime_failed"
			}
			return empty, &agentsdk.ConversationCodeFailure{Code: code}
		default:
			return empty, &agentsdk.ConversationCodeFailure{Code: "code_protocol_failed"}
		}
	}
}

var _ agentsdk.ConversationCodeRuntime = (*Process)(nil)
