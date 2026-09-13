package composition

import (
	"context"

	lifecyclemodulehost "github.com/domainry/domainry-lifecycle-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func lifecycleActionContext(ctx context.Context) context.Context {
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return ctx
	}
	return lifecyclemodulehost.WithExecutor(ctx, executor)
}

func bindActionVerifyFileClean(port func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)) func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
	if port == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID string, request runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
		return port(lifecycleActionContext(ctx), workspaceID, request)
	}
}

func bindActionOpenVerifiedFile(port func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error)) func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
	if port == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID string, request runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
		return port(lifecycleActionContext(ctx), workspaceID, request)
	}
}

func bindActionIssueFileDownload(port func(context.Context, string, runtimeext.Principal, runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error)) func(context.Context, string, runtimeext.Principal, runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error) {
	if port == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID string, principal runtimeext.Principal, request runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error) {
		return port(lifecycleActionContext(ctx), workspaceID, principal, request)
	}
}

func bindActionCreateDerivedFile(port func(context.Context, string, runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error)) func(context.Context, string, runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
	if port == nil {
		return nil
	}
	return func(ctx context.Context, workspaceID string, request runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
		return port(lifecycleActionContext(ctx), workspaceID, request)
	}
}
