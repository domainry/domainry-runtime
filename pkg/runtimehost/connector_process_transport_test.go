package runtimehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
)

func TestConnectorProcessTransportRequiresHostAuthorizationAndBoundsMessages(t *testing.T) {
	executable := "/bin/cat"
	if _, err := os.Stat(executable); err != nil {
		t.Skipf("test executable unavailable: %v", err)
	}
	request := connector.ProcessRequest{Executable: executable, MaxMessageBytes: 32}
	denied := newConnectorTransport().(connector.ProcessTransport)
	if _, err := denied.StartProcess(t.Context(), request); err == nil {
		t.Fatal("zero host policy authorized a process")
	}
	allowed := newConnectorTransportWithProcessPolicy(ConnectorProcessPolicy{AllowedExecutables: []string{executable}, AllowInheritedWorkingDirectory: true}).(connector.ProcessTransport)
	session, err := allowed.StartProcess(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SendLine(t.Context(), []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatal(err)
	}
	line, err := session.ReceiveLine(t.Context())
	if err != nil || string(line) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if err := session.SendLine(t.Context(), []byte("line\nbreak")); err == nil {
		t.Fatal("accepted multiline process message")
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorProcessTransportConfinesWorkingDirectories(t *testing.T) {
	executable := "/bin/cat"
	if _, err := os.Stat(executable); err != nil {
		t.Skipf("test executable unavailable: %v", err)
	}
	root := t.TempDir()
	inside := filepath.Join(root, "project")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	transport := newConnectorTransportWithProcessPolicy(ConnectorProcessPolicy{AllowedExecutables: []string{executable}, AllowedWorkingDirectoryRoots: []string{root}}).(connector.ProcessTransport)
	if _, err := transport.StartProcess(t.Context(), connector.ProcessRequest{Executable: executable, WorkingDirectory: t.TempDir(), MaxMessageBytes: 32}); err == nil {
		t.Fatal("authorized a working directory outside the host root")
	}
	session, err := transport.StartProcess(t.Context(), connector.ProcessRequest{Executable: executable, WorkingDirectory: inside, MaxMessageBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := session.SendLine(ctx, []byte("value")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	_ = session.Close(t.Context())
}
