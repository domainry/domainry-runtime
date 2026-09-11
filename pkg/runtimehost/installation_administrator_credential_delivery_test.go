package runtimehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallationAdministratorCredentialFileDeliveryIsPrivateAndCreateOnly(t *testing.T) {
	directory := privateTempDir(t)
	path := filepath.Join(directory, "installation-administrator.json")
	delivery, err := NewInstallationAdministratorCredentialFileDelivery(path)
	if err != nil {
		t.Fatal(err)
	}
	credential := InstallationAdministratorCredential{
		CanonicalWorkspaceCode: "primary", LoginID: "installation@example.test",
		InitialPassword: "OneTimeSecret!", MustChangePassword: true,
	}
	acknowledgment, err := delivery.DeliverInstallationAdministratorCredential(t.Context(), credential)
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
	if err := json.Unmarshal(raw, &payload); err != nil || payload["workspace_code"] != "primary" || payload["initial_password"] != credential.InitialPassword {
		t.Fatalf("payload=%#v error=%v", payload, err)
	}
	if _, err := NewInstallationAdministratorCredentialFileDelivery(path); err == nil {
		t.Fatal("existing installation administrator credential target was accepted")
	}
}
