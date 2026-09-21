package capability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type agentGuideIndex struct {
	ContractVersion string       `json:"contract_version"`
	Guides          []agentGuide `json:"guides"`
}

type agentGuide struct {
	Key                    string   `json:"key"`
	Path                   string   `json:"path"`
	ProvidedCapabilityKeys []string `json:"provided_capability_keys"`
	RequiredCapabilityKeys []string `json:"required_capability_keys"`
	EmbeddedCapabilityKeys []string `json:"embedded_capability_keys"`
	UsageKind              string   `json:"usage_kind"`
	RelatedGuides          []string `json:"related_guides"`
}

func TestAgentGuideIndexReferencesPublishedRuntimeCapabilitiesAndFiles(t *testing.T) {
	root := capabilityRepositoryRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "capability", "agent", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index agentGuideIndex
	if err := json.Unmarshal(payload, &index); err != nil {
		t.Fatalf("decode agent guide index: %v", err)
	}
	if index.ContractVersion != "domainry-agent-guide-index-v3" {
		t.Fatalf("agent guide contract version=%q", index.ContractVersion)
	}

	published := map[string]bool{}
	runtimeDomains := map[string]bool{}
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		runtimeDomains[domain.Key] = true
		for _, definition := range domain.Capabilities {
			published[definition.Key] = true
		}
	}
	guides := map[string]bool{}
	for _, guide := range index.Guides {
		if guide.Key == "" || guides[guide.Key] {
			t.Fatalf("agent guide key is blank or duplicated: %q", guide.Key)
		}
		guides[guide.Key] = true
		path := filepath.Clean(filepath.Join(root, guide.Path))
		if path == root || !strings.HasPrefix(path, root+string(filepath.Separator)) {
			t.Fatalf("agent guide %q escapes repository root: %q", guide.Key, guide.Path)
		}
		if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
			t.Fatalf("agent guide %q path=%q err=%v", guide.Key, guide.Path, statErr)
		}
		if guide.UsageKind != "selection" && guide.UsageKind != "authoring" && guide.UsageKind != "operations" && guide.UsageKind != "hybrid" && guide.UsageKind != "extension" {
			t.Fatalf("agent guide %q has invalid usage_kind %q", guide.Key, guide.UsageKind)
		}
		keys := append([]string(nil), guide.ProvidedCapabilityKeys...)
		keys = append(keys, guide.RequiredCapabilityKeys...)
		keys = append(keys, guide.EmbeddedCapabilityKeys...)
		for _, key := range keys {
			domain, _, found := strings.Cut(key, ".")
			if found && runtimeDomains[domain] && !published[key] {
				t.Fatalf("agent guide %q references unpublished Runtime capability %q", guide.Key, key)
			}
		}
	}
	for _, guide := range index.Guides {
		for _, related := range guide.RelatedGuides {
			if !guides[related] {
				t.Fatalf("agent guide %q references missing related guide %q", guide.Key, related)
			}
		}
	}

	requireGuideCapability(t, index.Guides, "business_calendar", "schema.business_calendar")
	requireGuideCapability(t, index.Guides, "workflow", "workflow.assignee_resolver")
	requireGuideCapability(t, index.Guides, "records", "schema.field")
	requireGuideText(t, root, "capability/agent/records.md", "`file`", "`file_list`", "`multi_select`", "`json`", "canonical JSON")
	requireGuideText(t, root, "capability/agent/business-calendar.md", "schema.business_calendar", "skip", "roll_forward", "immutable")
}

func capabilityRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve capability guide test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func requireGuideCapability(t *testing.T, guides []agentGuide, guideKey, capabilityKey string) {
	t.Helper()
	for _, guide := range guides {
		if guide.Key != guideKey {
			continue
		}
		for _, key := range append(append([]string(nil), guide.ProvidedCapabilityKeys...), guide.EmbeddedCapabilityKeys...) {
			if key == capabilityKey {
				return
			}
		}
		t.Fatalf("agent guide %q does not disclose capability %q", guideKey, capabilityKey)
	}
	t.Fatalf("agent guide %q is missing", guideKey)
}

func requireGuideText(t *testing.T, root, name string, values ...string) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("guide %q does not disclose %q", name, value)
		}
	}
}
