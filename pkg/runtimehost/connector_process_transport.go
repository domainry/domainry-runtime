package runtimehost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
)

const maxConnectorProcessMessageBytes = int64(4 << 20)

func (transport *connectorTransport) StartProcess(ctx context.Context, request connector.ProcessRequest) (connector.ProcessSession, error) {
	if transport == nil {
		return nil, errors.New("Connector process transport is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	executable, err := authorizedConnectorExecutable(request.Executable, transport.processPolicy.AllowedExecutables)
	if err != nil {
		return nil, err
	}
	directory, err := authorizedConnectorWorkingDirectory(request.WorkingDirectory, transport.processPolicy)
	if err != nil {
		return nil, err
	}
	limit := request.MaxMessageBytes
	if limit < 1 || limit > maxConnectorProcessMessageBytes {
		return nil, fmt.Errorf("Connector process message limit must be between 1 and %d bytes", maxConnectorProcessMessageBytes)
	}
	grace := request.ShutdownGrace
	if grace <= 0 {
		grace = 500 * time.Millisecond
	}
	if grace > 5*time.Second {
		return nil, errors.New("Connector process shutdown grace exceeds five seconds")
	}
	command := exec.CommandContext(ctx, executable, append([]string(nil), request.Arguments...)...)
	command.Dir = directory
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Connector process stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Connector process stdout: %w", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start Connector process: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), int(limit))
	return &connectorProcessSession{command: command, stdin: stdin, scanner: scanner, maxMessageBytes: limit, shutdownGrace: grace}, nil
}

type connectorProcessSession struct {
	command         *exec.Cmd
	stdin           io.WriteCloser
	scanner         *bufio.Scanner
	maxMessageBytes int64
	shutdownGrace   time.Duration
	closeOnce       sync.Once
	closeErr        error
}

func (session *connectorProcessSession) SendLine(ctx context.Context, message []byte) error {
	if session == nil || session.stdin == nil {
		return errors.New("Connector process session is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(message) == 0 || int64(len(message)) > session.maxMessageBytes || strings.ContainsAny(string(message), "\r\n") {
		return errors.New("Connector process message is empty, oversized, or not one line")
	}
	if _, err := session.stdin.Write(append(append([]byte(nil), message...), '\n')); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("write Connector process message: %w", err)
	}
	return nil
}

func (session *connectorProcessSession) ReceiveLine(ctx context.Context) ([]byte, error) {
	if session == nil || session.scanner == nil {
		return nil, errors.New("Connector process session is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session.scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return append([]byte(nil), session.scanner.Bytes()...), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := session.scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Connector process message: %w", err)
	}
	return nil, io.EOF
}

func (session *connectorProcessSession) Close(ctx context.Context) error {
	if session == nil {
		return nil
	}
	session.closeOnce.Do(func() {
		_ = session.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- session.command.Wait() }()
		select {
		case session.closeErr = <-done:
		case <-ctx.Done():
			_ = session.command.Process.Kill()
			<-done
			session.closeErr = ctx.Err()
		case <-time.After(session.shutdownGrace):
			_ = session.command.Process.Kill()
			<-done
		}
	})
	return session.closeErr
}

func authorizedConnectorExecutable(value string, allowed []string) (string, error) {
	requested := strings.TrimSpace(value)
	if requested == "" || !filepath.IsAbs(requested) || strings.ContainsAny(requested, "\r\n\x00") {
		return "", errors.New("Connector process executable must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("resolve Connector process executable: %w", err)
	}
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		permitted, resolveErr := filepath.EvalSymlinks(filepath.Clean(candidate))
		if resolveErr == nil && permitted == resolved {
			return resolved, nil
		}
	}
	return "", errors.New("Connector process executable is not authorized by host policy")
}

func authorizedConnectorWorkingDirectory(value string, policy ConnectorProcessPolicy) (string, error) {
	requested := strings.TrimSpace(value)
	if requested == "" {
		if policy.AllowInheritedWorkingDirectory {
			return "", nil
		}
		return "", errors.New("Connector process inherited working directory is not authorized")
	}
	if !filepath.IsAbs(requested) {
		return "", errors.New("Connector process working directory must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("resolve Connector process working directory: %w", err)
	}
	for _, root := range policy.AllowedWorkingDirectoryRoots {
		root = strings.TrimSpace(root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		permitted, resolveErr := filepath.EvalSymlinks(filepath.Clean(root))
		if resolveErr != nil {
			continue
		}
		relative, relativeErr := filepath.Rel(permitted, resolved)
		if relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", errors.New("Connector process working directory is not authorized by host policy")
}
