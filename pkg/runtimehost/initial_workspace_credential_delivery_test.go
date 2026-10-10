package runtimehost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInitialWorkspaceCredentialFileDeliveryPreservesDisabledPasswordRotation(t *testing.T) {
	directory := privateTempDir(t)
	path := filepath.Join(directory, "initial-workspace-credential.json")
	delivery, err := NewInitialWorkspaceCredentialFileDelivery(path)
	if err != nil {
		t.Fatal(err)
	}

	want := struct {
		WorkspaceID        string `json:"workspace_id"`
		WorkspaceCode      string `json:"workspace_code"`
		LoginID            string `json:"login_id"`
		InitialPassword    string `json:"initial_password"`
		MustChangePassword bool   `json:"must_change_password"`
	}{
		WorkspaceID:        "workspace-1",
		WorkspaceCode:      "primary",
		LoginID:            "admin@example.test",
		InitialPassword:    "Domainry!123",
		MustChangePassword: false,
	}
	acknowledgment, err := delivery.DeliverInitialWorkspaceCredential(context.Background(), InitialWorkspaceCredential{
		WorkspaceID:        want.WorkspaceID,
		CanonicalCode:      want.WorkspaceCode,
		LoginID:            want.LoginID,
		InitialPassword:    want.InitialPassword,
		MustChangePassword: want.MustChangePassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !acknowledgment.Accepted {
		t.Fatal("initial Workspace credential delivery was not accepted")
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := struct {
		WorkspaceID        string `json:"workspace_id"`
		WorkspaceCode      string `json:"workspace_code"`
		LoginID            string `json:"login_id"`
		InitialPassword    string `json:"initial_password"`
		MustChangePassword bool   `json:"must_change_password"`
	}{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credential mismatch: got %#v, want %#v", got, want)
	}
}
