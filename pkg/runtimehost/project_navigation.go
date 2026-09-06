package runtimehost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func loadProjectNavigationCatalog(path string, readFile func(string) ([]byte, error)) (identitysdk.ProjectNavigationCatalog, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return identitysdk.NormalizeProjectNavigationCatalog(identitysdk.ProjectNavigationCatalog{
			ContractVersion: identitysdk.ProjectNavigationContractVersion,
			Menus:           []identitysdk.ProjectMenuDefinition{},
		})
	}
	if readFile == nil {
		return identitysdk.ProjectNavigationCatalog{}, fmt.Errorf("read project navigation template: dependency is unavailable")
	}
	payload, err := readFile(path)
	if err != nil {
		return identitysdk.ProjectNavigationCatalog{}, fmt.Errorf("read project navigation template %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var catalog identitysdk.ProjectNavigationCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return identitysdk.ProjectNavigationCatalog{}, fmt.Errorf("decode project navigation template %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing JSON value")
		}
		return identitysdk.ProjectNavigationCatalog{}, fmt.Errorf("decode project navigation template %q: %w", path, err)
	}
	normalized, err := identitysdk.NormalizeProjectNavigationCatalog(catalog)
	if err != nil {
		return identitysdk.ProjectNavigationCatalog{}, fmt.Errorf("validate project navigation template %q: %w", path, err)
	}
	return normalized, nil
}
