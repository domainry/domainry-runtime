# Verify a managed Knowledge reference before a business effect

Project composition may call `runtimehost.CheckKnowledgeDocumentSnapshot`
through the Runtime-owned `ConnectorGateway`. Adapt it to a narrow typed
capability in `internal/runtimebindings`; Handler logic never receives the
gateway, Knowledge SDK service or a repository.

The request selects one library and an SDK `KnowledgeDocumentSnapshot`: exact
document ID, content version, index generation, SHA256 and optional page/version.
Runtime derives the current Runtime/Workspace/user/role from `ActionExecution`
and permits the operation only during prewrite. No authority or source secret
is accepted from the request. Runtime invokes the optional Knowledge Owner
snapshot capability, then verifies the returned identity, versions and ready,
indexed, clean state before projecting metadata only.

The embedded Knowledge Owner rechecks current library download rights,
source-record/page policy and its managed-document lifecycle without reading
storage or calling the remote search/fetch Provider. The check is suitable for
the business transaction's local source guard. It does not prove current
external-provider search results or authorize customer disclosure. The caller
must bind the original model output and citations, enforce current Case state
and review policy, then enqueue its existing reliable channel delivery.

Unsupported owner/deployment bindings fail as unavailable. There is no fallback
to ordinary document metadata Get or a default Provider. This optional port
does not add an HTTP endpoint, database model or second Knowledge store.
