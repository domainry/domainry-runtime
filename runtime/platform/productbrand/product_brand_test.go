package productbrand

import "testing"

func TestResolveNameUsesGeneratedDefaultAndDeploymentOverride(t *testing.T) {
	if got := ResolveName(" "); got != "Domainry" {
		t.Fatalf("blank product brand name = %q", got)
	}
	if got := ResolveName(" Acme "); got != "Acme" {
		t.Fatalf("overridden product brand name = %q", got)
	}

	t.Setenv(NameEnvironmentVariable, " Runtime Acme ")
	if got := NameFromEnvironment(); got != "Runtime Acme" {
		t.Fatalf("environment product brand name = %q", got)
	}
	if NameRevision("Acme") == NameRevision("Domainry") {
		t.Fatal("distinct product brand names must have distinct revisions")
	}
}
