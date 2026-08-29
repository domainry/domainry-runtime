package dataexchange

import "testing"

func TestApplicationAndScopeContractsFailClosed(t *testing.T) {
	if err := (ApplicationRef{}).Validate(); err == nil {
		t.Fatal("empty application identity was accepted")
	}
	if err := (ApplicationRef{ApplicationID: "app", RuntimeID: "runtime"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Scope{}).Validate(); err == nil {
		t.Fatal("empty scope was accepted")
	}
	if err := (Scope{WorkspaceID: "workspace", ActorID: "actor"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
