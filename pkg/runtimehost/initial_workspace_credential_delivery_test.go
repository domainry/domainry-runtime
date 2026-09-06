package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identitymodule "github.com/domainry/domainry-identity/module"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

type initialCredentialDeliveryProbe struct {
	calls      int
	credential InitialWorkspaceCredential
	accepted   bool
	err        error
}

func (probe *initialCredentialDeliveryProbe) DeliverInitialWorkspaceCredential(_ context.Context, credential InitialWorkspaceCredential) (InitialWorkspaceCredentialDeliveryAcknowledgment, error) {
	probe.calls++
	probe.credential = credential
	return InitialWorkspaceCredentialDeliveryAcknowledgment{Accepted: probe.accepted}, probe.err
}

func TestInitialWorkspaceCredentialFileDeliveryIsPrivateCreateOnlyAndNeverOverwrites(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "initial-workspace.json")
	delivery, err := NewInitialWorkspaceCredentialFileDelivery(path)
	if err != nil {
		t.Fatal(err)
	}
	credential := InitialWorkspaceCredential{CanonicalCode: "primary", LoginID: "owner@example.test", InitialPassword: "OneTimeSecret!", MustChangePassword: true}
	acknowledgment, err := delivery.DeliverInitialWorkspaceCredential(t.Context(), credential)
	if err != nil || !acknowledgment.Accepted {
		t.Fatalf("ack=%+v error=%v", acknowledgment, err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("credential file mode=%v error=%v", info.Mode(), err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload["initial_password"] != credential.InitialPassword || payload["workspace_code"] != "primary" {
		t.Fatalf("payload=%#v error=%v", payload, err)
	}
	if _, err := delivery.DeliverInitialWorkspaceCredential(t.Context(), InitialWorkspaceCredential{InitialPassword: "ReplacementSecret!"}); err == nil || strings.Contains(err.Error(), "ReplacementSecret") {
		t.Fatalf("second delivery error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatalf("credential file was overwritten: %v", err)
	}
	if _, err := NewInitialWorkspaceCredentialFileDelivery(path); err == nil {
		t.Fatal("existing credential target was accepted")
	}
}

func TestWorkspaceManagerDeliversCredentialOnceAndDeliveryFailureKeepsCommittedWorkspace(t *testing.T) {
	for _, test := range []struct {
		name     string
		accepted bool
		failure  error
		wantErr  bool
	}{
		{name: "acknowledged", accepted: true},
		{name: "delivery failure", failure: errors.New("sink unavailable"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := serverTestConfig()
			cfg.DatabaseDriver = "sqlite"
			cfg.DBPath = filepath.Join(t.TempDir(), "workspace-credential.db")
			database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			factory := identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath})
			probe := &initialCredentialDeliveryProbe{accepted: test.accepted, err: test.failure}
			manager, err := newProjectWorkspaceManager(t.Context(), cfg, factory, database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), probe)
			if err != nil {
				t.Fatal(err)
			}
			manifest := manifestmodel.ManifestSchema{Roles: workspaceRolesForTest(), InitialWorkspaceAdministratorRole: "headquarters_admin"}
			activateErr := manager.Activate(t.Context(), manifest, nil)
			var deliveryErr *InitialWorkspaceCredentialDeliveryError
			if test.wantErr != errors.As(activateErr, &deliveryErr) {
				t.Fatalf("activate error=%v", activateErr)
			}
			if probe.calls != 1 || strings.TrimSpace(probe.credential.InitialPassword) == "" || manager.Binding() == nil {
				t.Fatalf("delivery calls=%d credential=%+v binding=%v", probe.calls, probe.credential, manager.Binding())
			}
			if err := manager.Activate(t.Context(), manifest, nil); err != nil || probe.calls != 1 {
				t.Fatalf("repeated activation error=%v calls=%d", err, probe.calls)
			}
			installation, found, err := workspaceprovision.LoadInstallation(t.Context(), database)
			if err != nil || !found || installation.WorkspaceID == "" {
				t.Fatalf("committed installation=%+v found=%t error=%v", installation, found, err)
			}
			if err := manager.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			restarted, err := newProjectWorkspaceManager(t.Context(), cfg, factory, database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), nil)
			if err != nil || restarted.Binding() == nil {
				t.Fatalf("restart manager=%v error=%v", restarted, err)
			}
			if err := restarted.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := database.CloseContext(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
