package integrationmodel

import (
	"fmt"
	"sort"
	"strings"
)

// RequiredConnectorSecretRefNames normalizes the connector definition's
// structural required-secret declaration without depending on Integration runtime behavior.
func RequiredConnectorSecretRefNames(connector ConnectorSchema) []string {
	seen := map[string]bool{}
	add := func(value any) {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			seen[text] = true
		}
	}
	switch values := connector.Config["required_secret_refs"].(type) {
	case []string:
		for _, value := range values {
			add(value)
		}
	case []any:
		for _, value := range values {
			add(value)
		}
	case string:
		for _, value := range strings.Split(values, ",") {
			add(value)
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
