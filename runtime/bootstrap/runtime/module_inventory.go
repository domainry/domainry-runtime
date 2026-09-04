package runtime

import (
	"errors"
	"sort"

	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/moduleinfo"
)

// ModuleInventory normalizes the independently versioned SDK descriptors into
// one control-plane contract. It reports the Binding that was actually opened,
// not the Factory configuration that was requested.
func (runtime *Runtime) ModuleInventory() (moduleinfo.Inventory, error) {
	if runtime == nil {
		return moduleinfo.Inventory{}, errors.New("Runtime is unavailable")
	}
	modules := make([]moduleinfo.Descriptor, 0, 9)
	add := func(key, mode string, capabilities []string, binding any) {
		persistence := moduleinfo.Persistence{Mode: moduleinfo.PersistenceBorrowedHost, SchemaOwner: key}
		if mode == string(moduleinfo.DeploymentModeSaaS) {
			persistence = moduleinfo.Persistence{Mode: moduleinfo.PersistenceServiceOwned, SchemaOwner: key}
		}
		if key == "monitoring" {
			persistence = moduleinfo.Persistence{Mode: moduleinfo.PersistenceNone}
		}
		modules = append(modules, moduleinfo.Descriptor{
			Key: key, Mode: moduleinfo.DeploymentMode(mode),
			Capabilities: append([]string(nil), capabilities...),
			HTTPAdapters: moduleHTTPAdapterInventory(binding),
			Persistence:  persistence,
		})
	}
	if binding := runtime.identityBinding; binding != nil {
		d := binding.Descriptor()
		add("identity", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.notificationBinding; binding != nil {
		d := binding.Descriptor()
		add("notification", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.integrationBinding; binding != nil {
		d := binding.Descriptor()
		add("integration", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.schedulerBinding; binding != nil {
		d := binding.Descriptor()
		add("scheduler", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.monitoringBinding; binding != nil {
		d := binding.Descriptor()
		add("monitoring", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.dataExchangeBinding; binding != nil {
		d := binding.Descriptor()
		add("data_exchange", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.agentBinding; binding != nil {
		d := binding.Descriptor()
		add("agent", string(d.Mode), d.Capabilities, binding)
	}
	if binding := runtime.lifecycleBinding; binding != nil {
		d, capabilities := binding.Descriptor(), []string{}
		if d.Capabilities.Governance {
			capabilities = append(capabilities, "governance")
		}
		if d.Capabilities.SubjectRequests {
			capabilities = append(capabilities, "subject_requests")
		}
		if d.Capabilities.RetentionWorker {
			capabilities = append(capabilities, "retention_worker")
		}
		if d.Capabilities.UploadArtifacts {
			capabilities = append(capabilities, "upload_artifacts")
		}
		if d.Capabilities.ArchiveEvidence {
			capabilities = append(capabilities, "archive_evidence")
		}
		add("lifecycle", string(d.Mode), capabilities, binding)
	}
	if binding := runtime.auditBinding; binding != nil {
		d, capabilities := binding.Descriptor(), []string{}
		if d.Capabilities.TransactionalAppend {
			capabilities = append(capabilities, "transactional_append")
		}
		if d.Capabilities.Query {
			capabilities = append(capabilities, "query")
		}
		if d.Capabilities.Export {
			capabilities = append(capabilities, "export")
		}
		if d.Capabilities.SubjectLifecycle {
			capabilities = append(capabilities, "subject_lifecycle")
		}
		if d.Capabilities.ArchiveReplication {
			capabilities = append(capabilities, "archive_replication")
		}
		add("audit", string(d.Mode), capabilities, binding)
	}
	if binding := runtime.metadataBinding; binding != nil {
		d := binding.Descriptor()
		add("metadata", string(d.Mode), append([]string(nil), d.Capabilities...), binding)
	}
	if binding := runtime.reportBinding; binding != nil {
		d := binding.Descriptor()
		add("report", string(d.Mode), append([]string(nil), d.Capabilities...), binding)
	}
	return moduleinfo.NewInventory(modules)
}

func moduleHTTPAdapterInventory(binding any) []moduleinfo.HTTPAdapter {
	provider, ok := binding.(modulehttp.Provider)
	if !ok || provider == nil {
		return nil
	}
	result := make([]moduleinfo.HTTPAdapter, 0, len(provider.HTTPAdapters()))
	for _, adapter := range provider.HTTPAdapters() {
		if adapter == nil {
			continue
		}
		routes := make([]string, 0, len(adapter.Routes()))
		for _, route := range adapter.Routes() {
			routes = append(routes, route.Pattern())
		}
		sort.Strings(routes)
		result = append(result, moduleinfo.HTTPAdapter{Name: adapter.Name(), Routes: routes})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
