package config

import "testing"

func TestRuntimeProductBrandNameUsesDefaultAndEnvironmentOverride(t *testing.T) {
	t.Setenv("PRODUCT_BRAND_NAME", "")
	if got := FromEnv().EffectiveProductBrandName(); got != "Domainry" {
		t.Fatalf("default product brand name = %q", got)
	}

	t.Setenv("PRODUCT_BRAND_NAME", " Acme ")
	if got := FromEnv().EffectiveProductBrandName(); got != "Acme" {
		t.Fatalf("environment product brand name = %q", got)
	}

	if got := (Config{ProductBrandName: " "}).EffectiveProductBrandName(); got != "Domainry" {
		t.Fatalf("blank Config product brand name = %q", got)
	}
}
