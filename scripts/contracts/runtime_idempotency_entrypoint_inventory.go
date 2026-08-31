//go:build ignore

// Regenerate with: go run scripts/contracts/runtime_idempotency_entrypoint_inventory.go
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const inventoryOutput = "docs/architecture/runtime-idempotency-entrypoint-inventory.md"

var mutationRoute = regexp.MustCompile(`mux\.HandleFunc\("((POST|PUT|PATCH|DELETE) [^"]+)"`)

type inventoryEntry struct {
	kind, owner, entrypoint, source, decision, keySource string
}

func main() {
	check := flag.Bool("check", false, "fail when the generated inventory is stale")
	flag.Parse()
	content := renderInventory(discoverHTTPMutations(), discoverApplicationCommands(), workerEntries(), externalEffectEntries())
	if *check {
		current, err := os.ReadFile(inventoryOutput)
		if err != nil || !bytes.Equal(current, content) {
			fmt.Fprintf(os.Stderr, "%s is stale; run go run scripts/contracts/runtime_idempotency_entrypoint_inventory.go\n", inventoryOutput)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(inventoryOutput, content, 0o644); err != nil {
		panic(err)
	}
}

func discoverHTTPMutations() []inventoryEntry {
	entries := []inventoryEntry{}
	err := filepath.WalkDir("runtime/transport/http", func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() || !strings.HasSuffix(path, "_routes.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		owner := filepath.Base(filepath.Dir(path))
		for _, match := range mutationRoute.FindAllStringSubmatch(string(raw), -1) {
			decision, keySource := classifyHTTP(match[2], strings.TrimPrefix(match[1], match[2]+" "))
			entries = append(entries, inventoryEntry{kind: "http", owner: owner, entrypoint: match[1], source: path, decision: decision, keySource: keySource})
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	sortEntries(entries)
	return entries
}

func classifyHTTP(method, path string) (string, string) {
	readOnlyFragments := []string{"/validate", "/preview", "/simulate", "/analysis/query", "/context"}
	for _, fragment := range readOnlyFragments {
		if strings.Contains(path, fragment) {
			return "not_applicable", "none"
		}
	}
	if strings.HasPrefix(path, "/auth/") && (strings.Contains(path, "/start") || strings.Contains(path, "/callback") || strings.Contains(path, "/verify") || path == "/auth/login" || path == "/auth/guest" || path == "/auth/refresh" || path == "/auth/logout" || path == "/auth/code/exchange") {
		return "not_applicable", "security protocol replay rules"
	}
	if strings.Contains(path, "/webhooks/") || strings.Contains(path, "/process-due") || strings.HasSuffix(path, "/status") {
		return "system_key_required", "provider event, resource transition, or claimed work identity"
	}
	if (strings.HasPrefix(path, "/business/notifications") || strings.HasPrefix(path, "/portal/notifications")) &&
		(strings.HasSuffix(path, "/read") || strings.HasSuffix(path, "/unread") || strings.HasSuffix(path, "/archive") || strings.HasSuffix(path, "/restore") || strings.HasSuffix(path, "/read-all") || method == "DELETE") {
		return "natural_key", "workspace plus current user, Surface, notification or saved-view identity, and target mailbox state"
	}
	if method == "PUT" {
		return "natural_key", "workspace plus stable path resource"
	}
	if method == "PATCH" || method == "DELETE" {
		return "optimistic_only", "workspace plus resource identity and expected version"
	}
	return "caller_key_required", "Idempotency-Key header"
}

func discoverApplicationCommands() []inventoryEntry {
	entries := []inventoryEntry{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("runtime/application", func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		owner, _ := filepath.Rel("runtime/application", filepath.Dir(path))
		owner = strings.Split(filepath.ToSlash(owner), "/")[0]
		for _, declaration := range file.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok || method.Recv == nil || !method.Name.IsExported() || !isApplicationServiceReceiver(method.Recv.List[0].Type) || !hasContextFirst(method.Type.Params) || !isMutationMethod(method.Name.Name) {
				continue
			}
			decision, keySource := classifyApplicationCommand(method.Name.Name)
			entries = append(entries, inventoryEntry{kind: "application", owner: owner, entrypoint: method.Name.Name, source: path, decision: decision, keySource: keySource})
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	sortEntries(entries)
	return entries
}

func hasContextFirst(params *ast.FieldList) bool {
	if params == nil || len(params.List) == 0 {
		return false
	}
	selector, ok := params.List[0].Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "context"
}

func isApplicationServiceReceiver(expression ast.Expr) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && strings.HasSuffix(identifier.Name, "ApplicationService")
}

func isMutationMethod(name string) bool {
	prefixes := []string{"Apply", "Approve", "Archive", "Assign", "Bind", "Cancel", "Change", "Create", "Delete", "Disable", "Dispatch", "Enable", "Enqueue", "Execute", "Expire", "Force", "Import", "Insert", "Invoke", "Process", "Publish", "Rebuild", "Record", "Reject", "Remove", "Replay", "Reset", "Resolve", "Restore", "Retry", "Revoke", "Rotate", "Run", "Save", "Set", "Transition", "Unbind", "Unlock", "Update", "Upsert", "Write"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func classifyApplicationCommand(name string) (string, string) {
	for _, prefix := range []string{"Process", "Rebuild", "Dispatch"} {
		if strings.HasPrefix(name, prefix) {
			return "system_key_required", "claimed work or deterministic operation identity"
		}
	}
	for _, prefix := range []string{"Set", "Update", "Upsert", "Delete", "Disable", "Enable", "Save"} {
		if strings.HasPrefix(name, prefix) {
			return "optimistic_only", "workspace plus aggregate identity and expected version"
		}
	}
	return "caller_key_required", "use-case key propagated from transport or parent execution"
}

func workerEntries() []inventoryEntry {
	return []inventoryEntry{
		{kind: "worker", owner: "workflow", entrypoint: "StartWorkflowWorker", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "workflow execution/process/node attempt identity"},
		{kind: "worker", owner: "scheduler", entrypoint: "SchedulerBinding.Start", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Scheduler-owned definition/window/run identity"},
		{kind: "worker", owner: "recordtimer", entrypoint: "startRecordTimerWorker -> RecordTimers.ProcessDueForAllWorkspaces", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Record Timer identity plus fencing token"},
		{kind: "worker", owner: "integration", entrypoint: "StartIntegrationEventWorker -> LocalWorkers.ProcessDueEvents", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Integration-owned workspace/provider/external event identity"},
		{kind: "worker", owner: "runtime", entrypoint: "StartPublicationHandoffWorker", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Runtime publication message and deduplication identity"},
		{kind: "worker", owner: "notification", entrypoint: "StartNotificationPublicationWorker", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "publication request identity"},
		{kind: "worker", owner: "notification", entrypoint: "startNotificationInboxWorker", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "workspace plus source and source_event_id materialization identity"},
		{kind: "worker", owner: "integration", entrypoint: "startConnectorProviderBackgroundWorker -> LocalWorkers.ProcessDueProviderTasks", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Integration-owned Provider task and fencing identity"},
		{kind: "worker", owner: "integration", entrypoint: "startIntegrationInvocationReconciliationWorker -> LocalWorkers.ProcessDueReconciliations", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "system_key_required", keySource: "Integration-owned invocation and reconciliation attempt identity"},
		{kind: "worker", owner: "integration", entrypoint: "startIntegrationCredentialExpiryWorker -> LocalWorkers.ProcessDueCredentialExpirations", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "natural_key", keySource: "Integration-owned workspace and secret identity"},
		{kind: "worker", owner: "metadata", entrypoint: "startMetadataSnapshotWatcher", source: "runtime/bootstrap/runtime/worker_lifecycle.go", decision: "natural_key", keySource: "latest durable metadata revision"},
	}
}

func externalEffectEntries() []inventoryEntry {
	return []inventoryEntry{
		{kind: "external_effect", owner: "publicationhandoff", entrypoint: "Integration Delivery.Accept", source: "runtime/application/publicationhandoff/publication_handoff_application_service.go", decision: "system_key_required", keySource: "persisted publication message and deduplication identity reused across retries"},
		{kind: "external_effect", owner: "notification", entrypoint: "notification_delivery", source: "runtime/domain/notification/runtime/notification_outbox_payload.go", decision: "system_key_required", keySource: "recipient/template/channel/dedupe key through Outbox"},
		{kind: "external_effect", owner: "auth", entrypoint: "identity_provider_exchange", source: "runtime/domain/auth/service/auth_external_identity.go", decision: "not_applicable", keySource: "provider security protocol state and nonce"},
	}
}

func renderInventory(groups ...[]inventoryEntry) []byte {
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Runtime Idempotency Entrypoint Inventory")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "> Generated by `go run scripts/contracts/runtime_idempotency_entrypoint_inventory.go`. Do not edit by hand.")
	fmt.Fprintln(&output, "> Every entry is an inventory decision, not proof that the target implementation already satisfies that decision.")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "Decision values: `caller_key_required`, `system_key_required`, `natural_key`, `optimistic_only`, `not_applicable`.")
	for _, entries := range groups {
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&output, "\n## %s (%d)\n\n", sectionTitle(entries[0].kind), len(entries))
		fmt.Fprintln(&output, "| Owner | Entrypoint | Decision | Key/source | Source |")
		fmt.Fprintln(&output, "|---|---|---|---|---|")
		for _, entry := range entries {
			fmt.Fprintf(&output, "| `%s` | `%s` | `%s` | %s | `%s` |\n", entry.owner, strings.ReplaceAll(entry.entrypoint, "|", "\\|"), entry.decision, entry.keySource, entry.source)
		}
	}
	return output.Bytes()
}

func sectionTitle(kind string) string {
	switch kind {
	case "http":
		return "HTTP mutation routes"
	case "application":
		return "Application mutation commands"
	case "worker":
		return "Process-owned workers"
	default:
		return "External side effects"
	}
}

func sortEntries(entries []inventoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].owner != entries[j].owner {
			return entries[i].owner < entries[j].owner
		}
		if entries[i].entrypoint != entries[j].entrypoint {
			return entries[i].entrypoint < entries[j].entrypoint
		}
		return entries[i].source < entries[j].source
	})
}
