package projection

import (
	"strings"
	"unicode"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// IntegrationUnmappedReadOnlyPrincipal fails closed. An external subject must
// be mapped to an Identity principal and resolved with an SDK AccessBundle;
// Runtime never fabricates a read-only role for an unknown identity.
func IntegrationUnmappedReadOnlyPrincipal(workspaceID, externalPrincipal string, objects map[string]definitionmodel.ObjectSchema) principalmodel.Principal {
	if strings.TrimSpace(workspaceID) == "" {
		workspaceID = principalmodel.InstallationWorkspaceID
	}
	if _, err := principalmodel.NewWorkspaceID(workspaceID); err != nil {
		workspaceID = ""
	}
	return principalmodel.Principal{Principal: identitysdk.Principal{UserID: "integration:unmapped:" + integrationSanitizePrincipalKey(externalPrincipal), WorkspaceID: workspaceID, Known: false}}
}

func integrationSanitizePrincipalKey(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
