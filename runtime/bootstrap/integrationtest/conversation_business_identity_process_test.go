package integrationtest

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/domainry/domainry-identity-sdk"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
	identitycapability "github.com/domainry/domainry-identity/capability"
)

// Only this opt-in acceptance owns the process. Runtime and Agent consume the
// public Identity SDK; neither imports the standalone service's internals.
type businessIdentityProcess struct {
	t                                                         *testing.T
	root, binary, database, endpoint, port, logPath, manifest string
	scope                                                     identity.ApplicationRef
	process                                                   *exec.Cmd
	done                                                      chan error
	log                                                       *os.File
	requests                                                  atomic.Int32
}

type businessIdentityTransport struct{ process *businessIdentityProcess }

func (t businessIdentityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.process.requests.Add(1)
	response, err := http.DefaultTransport.RoundTrip(r)
	if err == nil && response.StatusCode >= 400 {
		t.process.t.Log("Identity HTTP rejected", r.URL.Path, response.StatusCode, "request count", t.process.requests.Load())
	}
	return response, err
}

func newBusinessIdentityProcess(t *testing.T, scope identity.ApplicationRef, businessManifest string) *businessIdentityProcess {
	t.Helper()
	runtimeRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := &businessIdentityProcess{t: t, root: filepath.Join(filepath.Dir(runtimeRoot), "domainry-identity"), binary: filepath.Join(dir, "identity-server"), database: filepath.Join(dir, "identity.db"), scope: scope, logPath: filepath.Join(dir, "identity-process.log")}
	// Standalone Identity owns its field-policy catalog. The trusted test
	// deployment supplies only the declared object/field metadata; records,
	// Actions, workflows, reports and business database access stay in Runtime.
	readManifest := func(path string) map[string]any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	manifest := readManifest(filepath.Join(p.root, "domainry.template.json"))
	manifest["objects"] = readManifest(businessManifest)["objects"]
	p.manifest = filepath.Join(dir, "identity-field-catalog.json")
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if evidence := os.Getenv("RUNTIME_BUSINESS_SERVICE_EVIDENCE_DIR"); evidence != "" {
		if err = os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		p.logPath = filepath.Join(evidence, "identity-process.log")
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", p.binary, "./cmd/identity-server")
	build.Dir = p.root
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual Identity: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.endpoint = "http://" + listener.Addr().String()
	_, p.port, err = net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	p.start()
	return p
}

func (p *businessIdentityProcess) start() {
	p.t.Helper()
	var err error
	p.log, err = os.OpenFile(p.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		p.t.Fatal(err)
	}
	p.process = exec.Command(p.binary)
	p.process.Dir = p.root
	// The service gets only synthetic acceptance settings, never inherited
	// browser tokens or real provider/service credentials.
	env := map[string]string{
		"PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME"), "TMPDIR": os.Getenv("TMPDIR"),
		"APP_ENV": "development", "DATABASE_DRIVER": "sqlite", "APP_DB_PATH": p.database,
		"TEMPLATE_MANIFEST": p.manifest, "HTTP_BIND_HOST": "127.0.0.1", "PORT": p.port,
		"AUTH_ISSUER": p.endpoint, "AUTH_AUDIENCE": string(p.scope.ApplicationKey), "IDENTITY_WORKSPACE_ID": string(p.scope.WorkspaceID),
		"AUTH_DEFAULT_PASSWORD": "Business-Browser-Initial!2026", "AUTH_JWT_SECRET": "f05-isolated-identity-signing-key-32-bytes",
		"IDENTITY_DATA_SECRET_KEY":                 "f05-isolated-identity-data-key-32-bytes",
		"IDENTITY_APPLICATION_SERVICE_CREDENTIALS": string(p.scope.WorkspaceID) + "/" + string(p.scope.ApplicationKey) + "=f05-isolated-identity-service-credential",
		// This acceptance repeatedly revalidates stored results and revokes
		// permissions in one minute. Keep a finite explicit test deployment
		// quota; production defaults and per-operation authorization are intact.
		"IDENTITY_APPLICATION_RATE_LIMIT_PER_MINUTE": "12000",
		"HTTP_PUBLIC_RATE_LIMIT_PER_MINUTE":          "15000",
		// Explicit deployment inventory for this Runtime fixture, without
		// Identity's builtin owner or a wildcard grant.
		"IDENTITY_APPLICATION_PERMISSION_OWNERS": string(p.scope.WorkspaceID) + "/" + string(p.scope.ApplicationKey) + "=application:" + string(p.scope.ApplicationKey) + "|runtime:appschema|runtime:automation|runtime:businessreferences|runtime:businesssystem|runtime:builtin|runtime:notifications|runtime:operations|runtime:records|runtime:workflows|runtime:workspaceprovision|module:agent|module:audit|module:integration|module:lifecycle|module:metadata|module:notification|module:report|module:scheduler|tools:user_preferences",
	}
	for key, value := range env {
		p.process.Env = append(p.process.Env, key+"="+value)
	}
	p.process.Stdout, p.process.Stderr = p.log, p.log
	if err = p.process.Start(); err != nil {
		p.t.Fatal(err)
	}
	p.done = make(chan error, 1)
	go func() { p.done <- p.process.Wait() }()
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-p.done:
			p.done <- err
			p.t.Fatalf("Identity process exited: %v; see %s", err, p.logPath)
		default:
		}
		response, err := client.Get(p.endpoint + "/identity/discovery")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Fatalf("Identity readiness timed out; see %s", p.logPath)
}

func (p *businessIdentityProcess) stop() {
	if p.process == nil {
		return
	}
	_ = p.process.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.process.Process.Kill()
		<-p.done
	}
	p.process = nil
	_ = p.log.Close()
}

func (p *businessIdentityProcess) factory() identity.Factory {
	p.t.Helper()
	capability, err := identitycapability.Open(identitycapability.Inputs{})
	if err != nil {
		p.t.Fatal(err)
	}
	summary, err := capability.CapabilitySummary(p.t.Context())
	if err != nil {
		p.t.Fatal(err)
	}
	return identityremote.NewFactory(identityremote.Config{
		Endpoint: p.endpoint, Issuer: p.endpoint, Audience: string(p.scope.ApplicationKey), WorkspaceID: string(p.scope.WorkspaceID),
		ServiceAccessToken: "f05-isolated-identity-service-credential", CapabilityContractSHA256: summary.Identity.ContractSHA256,
		HTTPClient: &http.Client{Transport: businessIdentityTransport{process: p}, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	})
}
