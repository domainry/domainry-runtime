# Module integration contract

Runtime integrates source-owned modules through an SDK `Factory` and the
resulting `Binding`. A Module factory borrows narrow host infrastructure; a
SaaS factory returns a remote Binding. Runtime must not select topology from
ambient environment variables.

## Shared rules

- Capabilities come from the descriptor of the Binding that actually opened.
- Module persistence borrows the Runtime pool and migration ledger, while each
  module remains the schema owner. SaaS persistence is service-owned.
- A module contributes inbound product HTTP only through
  `modulehttp.Provider`. Runtime validates exposure, authentication,
  permissions and route collisions before mounting it.
- SaaS Bindings never contribute in-process HTTP Surfaces.
- Runtime orchestration and BFF endpoints stay in Runtime even when they call a
  module Binding. Remote SDK `/v1` service protocols are not product HTTP and
  are never mounted into the Runtime listener.
- The active result is available from `GET /operations/modules` using the
  `domainry-module-inventory-v1` contract. Plane validates this handshake; it
  does not infer active capabilities from environment configuration.

## Current ownership

| Module | Module HTTP Surface | Runtime-owned HTTP that remains | Persistence |
| --- | --- | --- | --- |
| Identity | Authentication, browser session and Identity management | Project provisioning orchestration | Borrowed pool; Identity-owned schema |
| Party | Party directory and Party foundation catalog | SaaS proxy facade only | Borrowed pool; Party-owned schema |
| Notification | None yet | Inbox product projections, SSE, publication orchestration and delivery governance | Borrowed pool; Notification-owned schema |
| Integration | Web Push readiness and subscription self-service | Durable Runtime outbox handoff query; SaaS compatibility proxy | Borrowed pool; Integration-owned schema |
| Scheduler | None | Runtime definition authoring, operation receipts and trigger acceptance | Borrowed pool; Scheduler-owned schema |
| Monitoring | Operations metrics | Process liveness/readiness/startup probes; SaaS compatibility proxy | None |
| Data Exchange | None | Record import/export and artifact orchestration | Borrowed pool; Data Exchange-owned schema |
| Agent | None | Dialog, task and proposal orchestration | Borrowed pool; Agent-owned schema |
| Lifecycle | None | Cross-owner retention, legal hold and subject-request orchestration | Borrowed pool; Lifecycle-owned schema |
| Audit | None | Business, tenant-governance and operations projections | Borrowed pool; Audit-owned schema |
| Metadata | None | Runtime authoring and schema projection | Borrowed pool; Metadata-owned schema |
| Report | None | Query, export, notification and download orchestration | Borrowed pool; Report-owned schema |

“None” is explicit ownership, not missing integration. If a module later owns
an independent inbound product protocol, the Binding may add a Surface without
changing Runtime composition. Endpoints that depend on Runtime records,
idempotency receipts, cross-module transactions, product surface projection or
operation control remain Runtime-owned.
