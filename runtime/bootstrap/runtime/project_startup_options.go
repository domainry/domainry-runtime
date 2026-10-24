package runtime

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgecontract "github.com/domainry/domainry-knowledge-sdk/contract"
	knowledgefiles "github.com/domainry/domainry-knowledge-sdk/files"
	knowledgemodulehost "github.com/domainry/domainry-knowledge-sdk/modulehost"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// ProjectStartupOptions carries trusted process-local inputs. None of these
// values are the decoded project model or persisted model metadata.
type ProjectStartupOptions struct {
	ProjectModel              projectmodel.RuntimeModel
	ConversationCodeRuntime   agentsdk.ConversationCodeRuntime
	ConversationCodingRuntime agentsdk.ConversationCodingRuntime
	ConversationToolsFactory  toolsdk.ConversationToolFactory
	DevelopmentData           DevelopmentDataOptions
	AnalysisTableSource       reportmodulehost.AnalysisTableSource
	KnowledgeFactory          knowledgemodulehost.Factory
	KnowledgeOptions          knowledgecontract.Options
	KnowledgeFiles            knowledgefiles.Service
	BlobStore                 runtimefile.BlobStore
	FileScanner               runtimefile.FileScanner
	ProjectHTTP               runtimeengine.HTTPFactory
}

type DevelopmentDataOptions struct {
	Enabled          bool
	Seed             int64
	RecordsPerObject int
}
