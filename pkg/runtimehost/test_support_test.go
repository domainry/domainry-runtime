package runtimehost

import (
	"context"
	"errors"
	"os"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type identityFactoryStub struct {
	binding identitysdk.Binding
	err     error
}

func (factory identityFactoryStub) Open(context.Context, identitysdk.ApplicationRef) (identitysdk.Binding, error) {
	if factory.err != nil {
		return nil, factory.err
	}
	if factory.binding != nil {
		return factory.binding, nil
	}
	return identityBindingStub{}, nil
}

func (factory identityFactoryStub) OpenBootstrapWithDatabase(context.Context, identitysdk.ApplicationKey, identitysdk.DatabaseHandle) (identitysdk.BootstrapBinding, error) {
	if factory.err != nil {
		return nil, factory.err
	}
	return nil, errors.New("identity bootstrap is not configured for this test")
}

type identityBindingStub struct {
	runtimetestkit.IdentityBindingStub
}

func (identityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeSaaS}
}

type acceptingCredentialDelivery struct{}

func (acceptingCredentialDelivery) DeliverInitialWorkspaceCredential(context.Context, InitialWorkspaceCredential) (InitialWorkspaceCredentialDeliveryAcknowledgment, error) {
	return InitialWorkspaceCredentialDeliveryAcknowledgment{Accepted: true}, nil
}

func privateTempDir(t interface {
	Helper()
	TempDir() string
	Fatal(...any)
}) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func serverTestConfig() config.Config {
	return config.Config{
		Port: ":0", TelemetryExportTimeout: time.Second,
		IdentityAudience: "domainry-runtime", InitialWorkspaceRequestID: "initial-workspace", InitialWorkspaceCode: "primary",
		InitialWorkspaceName: "Primary", InitialWorkspaceFirstStoreCode: "primary-store", InitialWorkspaceFirstStoreName: "Primary Store",
		InitialWorkspaceAdminLoginID: "admin@example.test", InitialWorkspaceAdminName: "Admin",
		InitialWorkspaceCommercialConfigurationJSON: `{"plan":"standard","included_user_limit":1,"max_user_limit":100,"included_customer_limit":0,"max_customer_limit":1000,"included_store_limit":1,"max_stores":2,"contract_date":"2026-09-06","billing_day":1,"billing_contact_name":"","billing_contact_phone":"","billing_contact_email":"","billing_contact_address":"","billing_contact_notes":""}`,
		HTTPReadHeaderTimeout:                       time.Second, HTTPReadTimeout: time.Second, HTTPWriteTimeout: time.Second,
		HTTPIdleTimeout: time.Second, HTTPShutdownTimeout: time.Second, HTTPMaxHeaderBytes: 1024,
	}
}
