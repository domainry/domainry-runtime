# Builder / Runtime Boundary

## Deployment model

Builder and Runtime are independently deployable processes. Builder authors and compiles business contracts; Runtime discovers supported capabilities and executes accepted contracts. A deployment records `contract_version` and `contract_hash`, and both sides fail closed when the declared contract is unsupported or the hash does not match the reviewed artifact.

## Data ownership

Builder owns source projects, route registries, design evidence and deployment records. Runtime owns operational metadata, records, identities, credentials, executions, schedules, reports and audit evidence. No process reads or writes the other process's database tables directly; all exchange crosses a versioned HTTP contract.

## Connector ownership boundary

`domainry-connectors` owns the Connector catalog, Provider schemas and Provider implementations. `domainry-integration` owns credential lifecycle, configured connections, operation execution and compensation evidence. Runtime owns only application requirement projections, its publication outbox handoff, and Action/Workflow execution reached through the Integration SDK. Builder never stores Provider credentials. Generated applications must not copy Connector backend implementations; they reference Connector and operation keys discovered from the capability contract.

## Runtime capability discovery contract

Builder consumes Runtime's versioned capability description before compilation. Unsupported capability keys, incompatible versions and unverifiable hashes fail closed. The source-owned frontend may render routes and journeys, but cannot redefine Runtime execution semantics.

Runtime provides a bounded three-step projection contract. Builder/CLI model-facing consumers must use it as follows:

1. Module summary and `RuntimeModelAPIContractIndex` disclose only selection keys and routing facts. They must be the default model context.
2. The caller requests one exact capability category or a selected set through `RuntimeModelAPIContractProjection`. Runtime authoring source projections contain only lifecycle, dependency, input schema and reference-binding facts needed to author candidate source values.
3. Full OpenAPI operations, HTTP paths and methods, headers, status codes, idempotency mechanics, SSE cursors, background-job state and artifact download routes remain exact machine/client contracts. They are not model context.

Runtime-owned automatic delivery policy stays below the model boundary. For example, a model selects `record_export` and sees a `file` result; Runtime chooses inline or background production from the effective export scope. Browser notification, progress, SSE resumption and the eventual artifact download are client/platform behavior and must not become separate model-authored branches.

Owner validation remains authoritative after projection compaction. Removed authoring fields such as example payloads, validation error catalogs, output envelopes and execution policy continue to exist in their source-owner contracts and live validation path; their removal from the model projection does not remove enforcement.
