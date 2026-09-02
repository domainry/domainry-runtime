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
- A SaaS Binding may contribute the same source-owned product HTTP Surface as
  Module mode when the Surface is the module's deployment-neutral adapter over
  its remote protocol. Runtime still only validates and mounts it; the SaaS
  protocol endpoints themselves are never exposed as product HTTP.
- Runtime orchestration and host-owned HTTP endpoints stay in Runtime even when
  they call a module Binding. Remote SDK `/v1` service protocols are not product HTTP and
  are never mounted into the Runtime listener.
- The active result is available from `GET /operations/modules` using the
  `domainry-module-inventory-v1` contract. Plane validates this handshake; it
  does not infer active capabilities from environment configuration.

## Current ownership

| Module | Module HTTP Surface | Runtime-owned HTTP that remains | Persistence |
| --- | --- | --- | --- |
| Identity | Authentication, browser session and Identity management | Project provisioning orchestration | Borrowed pool; Identity-owned schema |
| Notification | None yet | Inbox product projections, SSE, publication orchestration and delivery governance | Borrowed pool; Notification-owned schema |
| Integration | Web Push readiness and subscription self-service | Durable Runtime outbox handoff query; SaaS compatibility proxy | Borrowed pool; Integration-owned schema |
| Scheduler | None | Runtime definition authoring, operation receipts and trigger acceptance | Borrowed pool; Scheduler-owned schema |
| Monitoring | Operations metrics | Process liveness/readiness/startup probes; SaaS compatibility proxy | None |
| Data Exchange | None | Record import/export and artifact orchestration | Borrowed pool; Data Exchange-owned schema |
| Agent | Dialog/session, proposal, interactive/task operations, analysis and diagnostics in both Module and SaaS Binding modes | Workflow state, current authorization and concrete host business effects exposed only through Agent Host Ports | Module borrows the pool; SaaS uses Agent-owned remote persistence |
| Lifecycle | Policy, legal hold, cleanup creation/preview, metrics, archive evidence, subject request, external erasure and deletion replay | Durable cleanup-job run through Runtime Operations receipts | Borrowed pool; Lifecycle-owned schema and migrations |
| Audit | None | Business, tenant-governance and operations projections | Borrowed pool; Audit-owned schema |
| Metadata | None | Runtime authoring and schema projection | Borrowed pool; Metadata-owned schema |
| Report | None | Query, export, notification and download orchestration | Borrowed pool; Report-owned schema |

“None” is explicit ownership, not missing integration. If a module later owns
an independent inbound product protocol, the Binding may add a Surface without
changing Runtime composition. Product-use-case ownership determines the
Surface owner: a source-owned handler may request current Runtime facts or
business effects through narrow Host Ports, but it may not import Runtime
services or participate in a cross-owner transaction.
