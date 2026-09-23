package auditmodulefixture

import (
	"context"
	"testing"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmodule "github.com/domainry/domainry-audit/module"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// Bind installs the source-owned Audit schema and binds its SDK contract to a
// RuntimeStore for tests that exercise cross-owner atomic mutations.
func Bind(t testing.TB, ctx context.Context, store *database.RuntimeStore) auditsdk.Binding {
	t.Helper()
	binding := EnsureBinding(ctx, store)
	t.Cleanup(func() { _ = binding.Close(context.WithoutCancel(ctx)) })
	return binding
}

// EnsureBinding equips an independently opened Runtime test store with the
// source-owned Audit module, matching production composition.
func EnsureBinding(ctx context.Context, store *database.RuntimeStore) auditsdk.Binding {
	if store == nil {
		panic("Runtime test Audit store is required")
	}
	if binding := store.Audit(); binding != nil {
		return binding
	}
	binding, err := auditmodule.NewFactory(auditmodule.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: "domainry-runtime-test"}, runtimeauditmodule.NewHost(store, nil, nil))
	if err != nil {
		panic("open Runtime test Audit module: " + err.Error())
	}
	if err := store.BindAudit(binding); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		panic("bind Runtime test Audit module: " + err.Error())
	}
	return binding
}
