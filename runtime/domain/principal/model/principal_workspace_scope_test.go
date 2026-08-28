package principalmodel

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestCommandAndQueryScopeRejectMissingWorkspace(t *testing.T) {
	for _, construct := range []func(string) error{
		func(value string) error { _, err := NewWorkspaceCommandScope(value); return err },
		func(value string) error { _, err := NewWorkspaceQueryScope(value); return err },
	} {
		if err := construct(" \t "); !errors.Is(err, ErrWorkspaceIDRequired) {
			t.Fatalf("missing workspace error=%v", err)
		}
		if err := construct(" workspace-a "); err != nil {
			t.Fatalf("valid workspace rejected: %v", err)
		}
	}

	command, err := NewWorkspaceCommandScope(" workspace-a ")
	if err != nil || !command.Valid() || command.WorkspaceID().String() != "workspace-a" || command.SystemScope().Valid() {
		t.Fatalf("unexpected command scope: %#v err=%v", command, err)
	}
	query, err := NewWorkspaceQueryScope("workspace-b")
	if err != nil || !query.Valid() || query.WorkspaceID().String() != "workspace-b" || query.SystemScope().Valid() {
		t.Fatalf("unexpected query scope: %#v err=%v", query, err)
	}
}

func TestCommandAndQueryScopeRequireValidExplicitSystemScope(t *testing.T) {
	if _, err := NewSystemCommandScope(SystemScope{}); !errors.Is(err, ErrSystemScopeRequired) {
		t.Fatalf("invalid command system scope error=%v", err)
	}
	if _, err := NewSystemQueryScope(NewSystemScope(SystemScopeBootstrap, "")); !errors.Is(err, ErrSystemScopeRequired) {
		t.Fatalf("invalid query system scope error=%v", err)
	}
	system := NewSystemScope(SystemScopeBootstrap, "manifest_activation")
	command, commandErr := NewSystemCommandScope(system)
	query, queryErr := NewSystemQueryScope(system)
	if commandErr != nil || queryErr != nil || !command.Valid() || !query.Valid() || command.WorkspaceID().Valid() || query.WorkspaceID().Valid() {
		t.Fatalf("explicit system scopes invalid: command=%#v query=%#v errors=%v/%v", command, query, commandErr, queryErr)
	}
}

func TestPrincipalScopeConstructionRejectsUnknownAndEmptyTenantPrincipal(t *testing.T) {
	for _, principal := range []Principal{{}, {Principal: identitysdk.Principal{Known: true}}} {
		if _, err := CommandScopeForPrincipal(principal); err == nil {
			t.Fatalf("invalid command principal accepted: %#v", principal)
		}
		if _, err := QueryScopeForPrincipal(principal); err == nil {
			t.Fatalf("invalid query principal accepted: %#v", principal)
		}
	}
	system := NewSystemPrincipal("bootstrap", NewSystemScope(SystemScopeBootstrap, "manifest"))
	if scope, err := CommandScopeForPrincipal(system); err != nil || !scope.SystemScope().Valid() {
		t.Fatalf("explicit system principal rejected: scope=%#v err=%v", scope, err)
	}
}
