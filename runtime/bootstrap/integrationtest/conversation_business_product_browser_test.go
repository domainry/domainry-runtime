package integrationtest

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
)

type businessServiceBrowserOptions struct {
	ConversationID   string
	Handler          func(string, fs.FS) (http.Handler, error)
	AssignRole       func(string)
	Restart          func()
	ServiceAvailable func(bool)
	Records          func() ([]agent.ConversationBusinessRecord, error)
}

// Test-only controls invoke the actual owners through the same public ports.
// This loopback server is never included in a product or production router.
func runBusinessServiceBrowser(t *testing.T, options businessServiceBrowserOptions) {
	t.Helper()
	runtimeRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	agentRoot := filepath.Join(filepath.Dir(runtimeRoot), "domainry-agent")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" || os.Getenv("AGENT_NODE_BINARY") == "" {
		t.Fatal("browser requires AGENT_UI_TEST_OUTPUT and AGENT_NODE_BINARY")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	files := os.DirFS(filepath.Join(agentRoot, "frontend/dist"))
	if _, err := fs.Stat(files, "index.html"); err != nil {
		t.Fatal("built Agent UI", err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := options.Handler(origin, files)
	if err != nil {
		t.Fatal(err)
	}
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/records":
				records, err := options.Records()
				if err != nil {
					http.Error(w, "records unavailable", 503)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(records)
				return
			case "/__acceptance/revoke-fields":
				options.AssignRole("business_field_restricted")
			case "/__acceptance/revoke-read":
				options.AssignRole("business_restricted")
			case "/__acceptance/restore":
				options.AssignRole("headquarters_admin")
			case "/__acceptance/unavailable":
				options.ServiceAvailable(false)
			case "/__acceptance/available":
				options.ServiceAvailable(true)
			case "/__acceptance/restart":
				options.Restart()
				var err error
				handler, err = options.Handler(origin, files)
				if err != nil {
					t.Error(err)
					http.Error(w, "handler unavailable", 503)
					return
				}
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(agentRoot, "frontend/tests/business-service.browser.mjs"))
	command.Dir = agentRoot
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_BUSINESS_QUERY_CONVERSATION="+options.ConversationID)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("actual shared Identity business browser", err)
	}
}
