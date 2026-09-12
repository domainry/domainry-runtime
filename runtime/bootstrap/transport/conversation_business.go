package transport

import (
	"crypto/sha256"
	"fmt"
	"strings"

	identity "github.com/domainry/domainry-identity-sdk"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
)

// ConversationBusinessDependencies is shared by embedded Agent composition and
// the opt-in business service. Runtime keeps its current owner implementations.
type ConversationBusinessDependencies struct {
	RuntimeID            string
	Application          identity.ApplicationScope
	Records              *composition.RuntimeServices
	Principals           identity.PrincipalResolver
	IntegrationSecretKey string
	IdentityIssuer       string
}

func NewConversationBusinessSource(d ConversationBusinessDependencies) (*agentapplication.ConversationBusinessHost, error) {
	if d.Records == nil || d.Principals == nil {
		return nil, fmt.Errorf("Runtime conversation business dependencies are incomplete")
	}
	applications := d.Records.Applications()
	if applications.Schema == nil || applications.Records == nil || applications.Actions == nil || applications.Workflows == nil {
		return nil, fmt.Errorf("Runtime conversation business owner ports are incomplete")
	}
	options := []agentapplication.ConversationBusinessHostOption{agentapplication.WithConversationBusinessActions(applications.Actions), agentapplication.WithConversationBusinessWorkflows(applications.Workflows)}
	if applications.Reports != nil && applications.Reports.Queries() != nil {
		options = append(options, agentapplication.WithConversationBusinessReports(conversationReportQueries{queries: applications.Reports.Queries()}))
	}
	if d.IdentityIssuer != "" {
		options = append(options, agentapplication.WithConversationBusinessIdentityIssuer(d.IdentityIssuer))
	}
	if strings.TrimSpace(d.IntegrationSecretKey) != "" {
		key := sha256.Sum256([]byte("domainry-agent-business-evidence-v1:" + d.IntegrationSecretKey))
		options = append(options, agentapplication.WithConversationBusinessEvidenceKey(key[:]))
	}
	return agentapplication.NewConversationBusinessHost(d.RuntimeID, d.Application, d.Principals, applications.Schema, applications.Records, options...)
}
