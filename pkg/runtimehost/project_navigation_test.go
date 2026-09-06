package runtimehost

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadProjectNavigationCatalogStrictlyLoadsAndNormalizesFile(t *testing.T) {
	catalog, err := loadProjectNavigationCatalog(" generated/navigation.json ", func(path string) ([]byte, error) {
		if path != "generated/navigation.json" {
			t.Fatalf("path=%q", path)
		}
		return []byte(`{
  "contract_version": "domainry-project-navigation-v1",
  "menus": [
    {"key":"orders.open","label":{"zh-CN":"待处理订单"},"route":"/orders/open","parent_key":"orders","sort_order":20},
    {"key":"orders","label":{"en":"Orders"},"route":"/orders","sort_order":10}
  ],
  "role_menu_sets": [{"role_key":"operator","menu_keys":["orders.open","orders","orders"]}]
}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Menus) != 2 || catalog.Menus[0].Key != "orders" || len(catalog.RoleMenuSets) != 1 || strings.Join(catalog.RoleMenuSets[0].MenuKeys, ",") != "orders,orders.open" {
		t.Fatalf("catalog=%+v", catalog)
	}
}

func TestLoadProjectNavigationCatalogRejectsMalformedOrUntrustedFiles(t *testing.T) {
	tests := []struct {
		name, payload, message string
	}{
		{"unknown field", `{"contract_version":"domainry-project-navigation-v1","menus":[],"application_key":"admin"}`, "unknown field"},
		{"trailing value", `{"contract_version":"domainry-project-navigation-v1","menus":[]} {}`, "trailing"},
		{"unknown menu relation", `{"contract_version":"domainry-project-navigation-v1","menus":[],"role_menu_sets":[{"role_key":"operator","menu_keys":["missing"]}]}`, "unknown menu"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadProjectNavigationCatalog("navigation.json", func(string) ([]byte, error) { return []byte(test.payload), nil })
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := loadProjectNavigationCatalog("navigation.json", func(string) ([]byte, error) { return nil, errors.New("missing") }); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("read error=%v", err)
	}
}

func TestLoadProjectNavigationCatalogWithoutPathUsesEmptyTemplate(t *testing.T) {
	catalog, err := loadProjectNavigationCatalog(" ", nil)
	if err != nil || catalog.ContractVersion != "domainry-project-navigation-v1" || len(catalog.Menus) != 0 || len(catalog.RoleMenuSets) != 0 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
}
