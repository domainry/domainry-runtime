# Who should own this business concept?

Start here when a requirement names a business noun but does not say which Domainry owner should control it. The important decision is the source of truth, not the screen or table name.

## Problems solved

- Prevents one business concept from being copied into project tables, Runtime metadata, and module state with competing lifecycle and authorization rules.

## Business scenarios

- Deciding whether tenant wording maps to the Runtime Workspace boundary or to an independent business Object.
- Separating login users and organization hierarchy from customer, order, and other project-owned business facts.

## Use when

- Before creating an Object, decide whether the concept already has a source owner and lifecycle.
- Use a project Object only for independent business facts, lifecycle, commands, or transaction rules.

## Do not use when

- Do not mirror Workspace, principal, organization hierarchy, credentials, schedules, Inbox state, or provider connections in project tables.
- Do not infer ownership from the frontend page name or an existing database table.

## How to use

1. Write the business fact and required lifecycle in plain language.
2. Match it to Runtime core or an installed module using the capability-guide index.
3. Check `effective-capabilities.json` before authoring Model JSON.
4. If the correct owner is not authorable, report a Contract Gap; do not replace it with a lookalike Object.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Employees sign in and also have payroll-specific facts. | Identity user plus a project-owned employee profile | Keep credentials, session, and principal in Identity; relate one employee profile to that Identity user for payroll facts. | Copying password, email login state, Role assignment, or the whole user into an employee Object. |
| Regions and stores place staff and control regional access, while stores also have commercial attributes. | Identity organization units plus an optional project store profile | Identity owns the region/store tree and placement; relate a store profile only for facts such as floor area or sales target. | Maintaining a second parent/child tree in project tables and trying to synchronize it. |
| A SaaS customer is called a tenant and owns customers and orders. | Runtime Workspace plus project customer/order Objects | Translate tenant isolation to Workspace context; model only the independent customer and order business lifecycle. | Creating `tenant`, `workspace`, `tenant_id`, or `workspace_id` project fields. |

## Example

Product request: “Each tenant has regions, stores, employees, customers, and orders.”

- Map “tenant” to the Runtime Workspace boundary; do not author a tenant Object or `tenant_id` field.
- Use Identity organization units for regions and stores when they drive user placement and organization data scope.
- Use Identity users for employees who authenticate.
- Use project Objects for customers and orders because they have independent business facts and lifecycle.
- If store also has domain-specific facts, model a store profile related to the Identity organization instead of copying the organization tree.

## Permissions and scope

Ownership selection happens before Role grants. Authorization cannot repair a duplicated source of truth.

## Boundaries

The exact accepted Model shape is always `model.schema.json`. Capability guides explain decisions but never expand that schema.
