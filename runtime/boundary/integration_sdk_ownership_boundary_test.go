package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationOwnershipContractKeepsOnlyRuntimeOutbox(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	content, err := os.ReadFile(filepath.Join(root, "docs", "modules", "integration.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{"_publication_outbox", "domainry-integration", "_schema_migrations", "SaaS Binding"} {
		if !strings.Contains(text, required) {
			t.Fatalf("Integration ownership contract is missing %q", required)
		}
	}
}

func TestRuntimeCompositionDoesNotReattachIntegrationOwnerState(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{
		filepath.Join(root, "runtime", "bootstrap", "runtime", "startup.go"),
		filepath.Join(root, "runtime", "bootstrap", "runtime", "service_assembly.go"),
		filepath.Join(root, "runtime", "bootstrap", "runtime", "worker_lifecycle.go"),
		filepath.Join(root, "runtime", "bootstrap", "transport", "http_integration_agent_handler_wiring.go"),
	}
	forbidden := []string{
		"NewIntegrationConfigStore(", "NewIntegrationEventStore(", "NewIntegrationSubjectLifecycleStore(",
		"NewIntegrationCredentialExpiryStore(", "StartEventWorker(", "StartConnectorBackgroundWorker(",
		"StartInvocationReconciliationWorker(", "ProcessCredentialExpiryNotifications(", "NewIntegrationsHandler(",
	}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Fatalf("Runtime composition %s reattaches Integration owner capability %q", path, fragment)
			}
		}
	}

	serviceAssembly, err := os.ReadFile(filepath.Join(root, "runtime", "bootstrap", "runtime", "service_assembly.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"IntegrationPublication:", "IntegrationPublicationWorker:"} {
		if !strings.Contains(string(serviceAssembly), required) {
			t.Fatalf("Runtime composition is missing narrow handoff port %q", required)
		}
	}
	publicationStore, err := os.ReadFile(filepath.Join(root, "runtime", "infrastructure", "persistence", "database", "publicationhandoff", "publication_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	workerStore, err := os.ReadFile(filepath.Join(root, "runtime", "infrastructure", "persistence", "database", "publicationhandoff", "worker_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	handoff := append(publicationStore, workerStore...)
	for _, forbidden := range []string{"database/integration\"", "IntegrationDeliveryStore", "IntegrationWorkerStore", "_integration_invocations", "_integration_events"} {
		if strings.Contains(string(handoff), forbidden) {
			t.Fatalf("Runtime publication handoff still depends on legacy Integration owner implementation %q", forbidden)
		}
	}
}
