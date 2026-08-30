# Domainry Runtime

Domainry Runtime is the independently versioned Go runtime consumed by
Domainry-generated business projects.

The Control Plane and Builder live in the sibling `domainry-plane` repository.
This repository contains only Runtime implementation and its public host and
business-extension APIs.

Runtime architecture and contribution rules are defined in
[`docs/architecture/backend-development-guide.md`](docs/architecture/backend-development-guide.md).
External capability extraction and Module/SaaS dual-topology rules are defined
in [`docs/architecture/module-saas-development-standard.md`](docs/architecture/module-saas-development-standard.md),
with the current capability inventory under [`docs/modules`](docs/modules/README.md).
