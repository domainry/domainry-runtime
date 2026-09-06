package action

import (
	"strings"
	"testing"
	"time"
)

func TestWorkspaceIdentityUsageCursorIsConfidentialBoundExpiringAndRestartStable(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	key := []byte("stable-workspace-identity-usage-secret")
	codec, err := NewWorkspaceIdentityUsageCursorCodec(key, "installation-a", clock)
	if err != nil {
		t.Fatal(err)
	}
	binding := WorkspaceIdentityUsageCursorBinding{ActionKey: "billing.invoice.generate", WorkspaceID: "workspace-hq", SubjectID: "billing-operator", AuthorizationRevision: "authz-7"}
	raw := `eyJhZnRlcl93b3Jrc3BhY2VfaWQiOiJ3b3Jrc3BhY2UtcGh5c2ljYWwtYSIsImluc3RhbGxhdGlvbl9pZCI6Imluc3RhbGxhdGlvbi1hIn0`
	sealed, err := codec.Seal(raw, binding)
	if err != nil {
		t.Fatal(err)
	}
	if sealed == raw || strings.Contains(sealed, "workspace-physical-a") || strings.Contains(sealed, "installation-a") {
		t.Fatalf("cursor leaked plaintext: %q", sealed)
	}
	restarted, err := NewWorkspaceIdentityUsageCursorCodec(key, "installation-a", clock)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := restarted.Open(sealed, binding); err != nil || opened != raw {
		t.Fatalf("restart open=%q err=%v", opened, err)
	}

	replacement := byte('A')
	if sealed[len(sealed)-1] == replacement {
		replacement = 'B'
	}
	tampered := sealed[:len(sealed)-1] + string(replacement)
	if _, err := codec.Open(tampered, binding); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
	for name, other := range map[string]WorkspaceIdentityUsageCursorBinding{
		"action":        {ActionKey: "billing.seat.preview", WorkspaceID: binding.WorkspaceID, SubjectID: binding.SubjectID, AuthorizationRevision: binding.AuthorizationRevision},
		"subject":       {ActionKey: binding.ActionKey, WorkspaceID: binding.WorkspaceID, SubjectID: "other-user", AuthorizationRevision: binding.AuthorizationRevision},
		"workspace":     {ActionKey: binding.ActionKey, WorkspaceID: "workspace-other", SubjectID: binding.SubjectID, AuthorizationRevision: binding.AuthorizationRevision},
		"authorization": {ActionKey: binding.ActionKey, WorkspaceID: binding.WorkspaceID, SubjectID: binding.SubjectID, AuthorizationRevision: "authz-8"},
	} {
		if _, err := codec.Open(sealed, other); err == nil {
			t.Fatalf("cross-%s cursor was accepted", name)
		}
	}
	otherInstallation, err := NewWorkspaceIdentityUsageCursorCodec(key, "installation-b", clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherInstallation.Open(sealed, binding); err == nil {
		t.Fatal("cross-installation cursor was accepted")
	}
	now = now.Add(workspaceIdentityUsageCursorTTL)
	if _, err := codec.Open(sealed, binding); err == nil {
		t.Fatal("expired cursor was accepted")
	}
}
