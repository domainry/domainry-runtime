package boundary_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Extracted business owners may be composed by generated applications and
// tests, but Runtime production code must consume their deployment-neutral
// SDKs instead of rebuilding or importing module/SaaS implementations.
func TestExtractedOwnersAreSDKOnlyInRuntimeProduction(t *testing.T) {
	root := runtimeRoot(t)
	forbiddenOwners := []string{
		"github.com/domainry/domainry-data-exchange",
		"github.com/domainry/domainry-notification",
		"github.com/domainry/domainry-party",
	}
	usedSDK := map[string]bool{
		"github.com/domainry/domainry-data-exchange-sdk": false,
		"github.com/domainry/domainry-notification-sdk":  false,
		"github.com/domainry/domainry-party-sdk":         false,
	}

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
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			for sdk := range usedSDK {
				if importPath == sdk || strings.HasPrefix(importPath, sdk+"/") {
					usedSDK[sdk] = true
				}
			}
			for _, owner := range forbiddenOwners {
				if importPath == owner || strings.HasPrefix(importPath, owner+"/") {
					t.Errorf("Runtime production code imports extracted owner implementation %q in %s", importPath, filepath.ToSlash(path))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for sdk, used := range usedSDK {
		if !used {
			t.Errorf("Runtime no longer consumes extracted owner SDK %q", sdk)
		}
	}
}

func TestNotificationAndPartyHistoricalOwnersStayRetired(t *testing.T) {
	root := runtimeRoot(t)
	for _, retired := range []string{
		"domain/notification/contract",
		"infrastructure/persistence/database/party",
	} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(retired))); err == nil && info.IsDir() {
			t.Errorf("retired extracted-owner implementation directory still exists: %s", retired)
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	notificationPersistence := filepath.Join(root, "infrastructure", "persistence", "database", "notification")
	entries, err := os.ReadDir(notificationPersistence)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != "inbox_event_writer.go" {
			t.Errorf("Notification-owned persistence returned to Runtime: %s", entry.Name())
		}
	}

	partyApplication := filepath.Join(root, "application", "party")
	err = filepath.WalkDir(partyApplication, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{"infrastructure/persistence", "database/", "github.com/domainry/domainry-party/"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("Party application contains implementation dependency %q in %s", forbidden, filepath.ToSlash(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
