package projection

import (
	"fmt"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationInvocationMetadataMatches(invocation integrationmodel.IntegrationInvocation, provider, externalPrincipal string) bool {
	if provider != "" {
		actual := strings.TrimSpace(invocation.ProviderKey)
		if actual == "" {
			actual = strings.TrimSpace(fmt.Sprint(invocation.Metadata["provider"]))
		}
		if actual != provider {
			return false
		}
	}
	return externalPrincipal == "" || strings.TrimSpace(fmt.Sprint(invocation.Metadata["external_principal"])) == externalPrincipal
}
