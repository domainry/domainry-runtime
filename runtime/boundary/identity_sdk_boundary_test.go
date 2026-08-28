package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runtime consumes Identity exclusively through domainry-identity-sdk. The
// Identity module and SaaS client are selected by the generated application;
// Plane must not grow another user/role/session/policy owner or import either
// deployment implementation.
func TestRuntimeIdentityBoundaryIsSDKOnly(t *testing.T) {
	root := runtimeRoot(t)
	for _, retired := range []string{
		"application/auth", "application/identity", "domain/auth",
		"domain/identity", "domain/principal/contract",
		"infrastructure/identityprovider",
		"infrastructure/persistence/database/auth", "infrastructure/persistence/database/identity",
		"transport/http/accesscontrol", "transport/http/auth", "transport/http/identity", "transport/http/identitybrowser",
	} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(retired))); err == nil && info.IsDir() {
			t.Errorf("retired Plane Identity owner still exists: %s", retired)
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	forbidden := []string{
		"github.com/domainry/domainry-identity/module",
		"github.com/domainry/domainry-identity-sdk/remote",
		"type Role struct", "type DataPermission struct", "type FieldPermission struct", "type ReferencePermission struct",
		"CommitIdentityRoleAuthorization", "SetIdentityRolePermissions",
		"identity_role_permission_assignments", "identity_data_scope_policies", "identity_field_permissions",
		"AuthProviderCredential", "auth_provider_credentials", "WriteSSOExternalIdentity", "backend.integration.sso",
	}
	usedSDK := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(raw)
		usedSDK = usedSDK || strings.Contains(content, "github.com/domainry/domainry-identity-sdk")
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				t.Errorf("Runtime Identity boundary contains forbidden implementation/legacy token %q in %s", token, filepath.ToSlash(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !usedSDK {
		t.Fatal("Runtime no longer consumes the Identity SDK contract")
	}
}

// SDK authorization fixtures are deliberately test-only. Keeping them under a
// package named for the SDK AccessBundle prevents Runtime from silently growing
// a second role/permission implementation behind an ambiguous principal helper.
func TestIdentitySDKFixtureIsTestOnly(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	legacyFixtureRoot := filepath.Join(repositoryRoot, "internal", "testsupport", "identity")
	if _, err := os.Stat(legacyFixtureRoot); err == nil {
		t.Fatalf("ambiguous legacy Identity fixture package still exists: %s", legacyFixtureRoot)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}

	fixtureImport := "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), fixtureImport) && !strings.HasSuffix(path, "_test.go") {
			t.Errorf("production code imports SDK authorization fixtures: %s", filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
