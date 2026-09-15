# Domainry Runtime

Domainry Runtime is the independently versioned Go runtime consumed by
Domainry-generated business projects.

The Control Plane and Builder live in the sibling `domainry-plane` repository.
This repository contains only Runtime implementation and its public host and
business-extension APIs.

Runtime architecture and contribution rules are defined in
[`docs/architecture/backend-development-guide.md`](docs/architecture/backend-development-guide.md).
The Runtime capability inventory, conventional-development cost comparison,
user-facing examples, and scenario-selection guide are documented in
[`docs/architecture/runtime-capability-and-development-guide.md`](docs/architecture/runtime-capability-and-development-guide.md).
External capability extraction and Module/SaaS dual-topology rules are defined
in [`docs/architecture/module-saas-development-standard.md`](docs/architecture/module-saas-development-standard.md),
with the current capability inventory under [`docs/modules`](docs/modules/README.md).

Generated project composition may explicitly set
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
