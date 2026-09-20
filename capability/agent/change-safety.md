# How should I inspect an existing system before changing resources?

Use Runtime's read-only maintenance capabilities to ground a change plan in the deployed system and its reference graph before mutating metadata.

## Problems solved

- Prevents an Agent from planning destructive metadata changes against stale state or overlooking definitions that reference the resource.

## Business scenarios

- Checking Workflow and Report consumers before deleting or renaming a field.
- Reading the deployed schema and snapshot hashes before preparing an upgrade to an existing system.

## Use when

- Read the current-state snapshot before planning changes to an existing system.
- Read reference impact before renaming, replacing, or deleting a resource that other definitions may consume.

## Do not use when

- Do not use maintenance discovery to choose the business owner of a new requirement.
- Do not treat a snapshot or impact graph as authorization to apply the proposed change.

## How to use

Call `maintenance.current_state_snapshot` to obtain the current schema, Runtime metadata, effective permissions, installed module state, and stable snapshot hashes. Call `maintenance.reference_impact` with one `resource_type` and `resource_key` to inspect direct dependencies, direct and indirect consumers, and whether deletion is blocked. Build the change plan from those results, then validate and apply changes through the owning authoring contracts.

## Adaptation cookbook

| Change request | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Delete `sales_order.legacy_status`. | Current-state snapshot plus reference impact | Confirm the deployed schema hash, inspect direct and indirect consumers, then update Workflow/Report owners before deletion. | Editing the model from memory and discovering broken references only after apply. |
| Replace or rename a public Operation. | Snapshot, reference graph, then owner validation | Find callers and dependent definitions, plan their coordinated change, and submit the new contract through its semantic owner. | Treating read-only impact discovery as permission to mutate or assuming a name search is complete. |

## Example

Before removing `sales_order.legacy_status`, first read the current-state snapshot and confirm the deployed schema hash. Then request reference impact for resource type `field` and resource key `sales_order.legacy_status`. If a Workflow condition and a Report definition consume the field, update those owners before attempting deletion.

## Permissions and scope

The caller needs `runtime.businesssystem.business_system_snapshot` for the snapshot and `runtime.businessreferences.business_reference_impact` for reference analysis. Both operations are read-only, but their results can expose system structure and must remain within the authorized Workspace.

## Boundaries

These capabilities discover current state and impact only. They do not select replacement semantics, mutate resources, bypass validation, or guarantee that a later write will still be conflict-free; use the returned hashes and the owning capability's validation and concurrency contract.
