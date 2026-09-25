# Domainry Runtime

Domainry Runtime is the independently versioned Go runtime consumed by
source-owned Domainry Go projects.

Plane publishes a signed Source Foundation once for a new Product; normal
Feature development then runs against the checked-in Model, Go source and
`go.mod/go.sum` without a Builder or Plane call. This repository contains only
Runtime implementation and its public host and business-extension APIs.

Agent-facing capability questions and scenario guides are indexed by the
source-owned [`capability/agent/index.json`](capability/agent/index.json). Each
leaf Markdown document answers one question so consumers can load only the
matching scenario from the locked Runtime release.

The authoritative machine-readable authoring contract is generated from code
by `capability.RuntimeAuthoringCapabilities()` and exposed by Runtime discovery;
it is not copied into a hand-maintained JSON file. The canonical catalog is
sorted and protected by `RuntimeAuthoringContractHash`, while tests verify that
Runtime-owned keys referenced by the guide index still exist. The guide index
is a scenario router, not a second authoring schema.

The Runtime capability inventory, conventional-development cost comparison,
user-facing examples, and scenario-selection guide are documented in
[`docs/architecture/runtime-capability-and-development-guide.md`](docs/architecture/runtime-capability-and-development-guide.md).
External capability extraction and Module/SaaS dual-topology rules are defined
in [`docs/architecture/module-saas-development-standard.md`](docs/architecture/module-saas-development-standard.md),
with the current capability inventory under [`docs/modules`](docs/modules/README.md).

Project composition may explicitly set
`runtimehost.Options.AgentCodingWorkspace` to a `codingruntime.Options` value.
This enables Agent's restricted coding workspace for one existing directory,
with a fixed shell and optional extension-to-language-server mappings. The zero
value leaves all coding tools unpublished. On Darwin the implementation requires
`/usr/bin/sandbox-exec`, denies subprocess network access and writes outside the
workspace, scrubs credentials from the child environment, and scopes PTYs and
background processes to the authenticated Agent run. Business and connector tools
remain in `domainry-tools`; this Runtime package owns only the local execution world.

Metadata is a source-owned embedded module. Runtime opens it once at startup,
binds the resulting `domainry-metadata-sdk.Binding`, mounts its declared HTTP
Adapter, and consumes only the SDK definition, localization, dictionary and
projection ports. Metadata migrations, tables, definition reads, localization
exports and dictionary endpoints are not implemented by Runtime.

Project business code enters Runtime only through
`runtimehost.Options.ProjectExtensions`. Business Handlers, Workspace bootstrap
participants, and Workflow assignee resolvers publish descriptors, freeze at
startup, and contribute to the Runtime release identity. Deployment
infrastructure is separate: `runtimehost.Options.BlobStore` and `FileScanner`
select storage and scanning adapters. Nil selects the local filesystem and
built-in scanner; these adapters never receive Runtime repositories and are not
project business extensions.
