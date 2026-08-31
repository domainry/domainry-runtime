# Builder / Runtime Boundary

## Deployment model

Builder and Runtime are independently deployable processes. Builder authors and compiles business contracts; Runtime discovers supported capabilities and executes accepted contracts. A deployment records `contract_version` and `contract_hash`, and both sides fail closed when the declared contract is unsupported or the hash does not match the reviewed artifact.

## Data ownership

Builder owns source projects, route registries, design evidence and deployment records. Runtime owns operational metadata, records, identities, credentials, executions, schedules, reports and audit evidence. No process reads or writes the other process's database tables directly; all exchange crosses a versioned HTTP contract.

## Connector ownership boundary

`domainry-connectors` owns the Connector catalog, Provider schemas and Provider implementations. `domainry-integration` owns credential lifecycle, configured connections, operation execution and compensation evidence. Runtime owns only application requirement projections, its publication outbox handoff, and Action/Workflow execution reached through the Integration SDK. Builder never stores Provider credentials. Generated applications must not copy Connector backend implementations; they reference Connector and operation keys discovered from the capability contract.

## Runtime capability discovery contract

Builder consumes Runtime's versioned capability description before compilation. Unsupported capability keys, incompatible versions and unverifiable hashes fail closed. The source-owned frontend may render routes and journeys, but cannot redefine Runtime execution semantics.
