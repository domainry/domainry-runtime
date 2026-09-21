# Who should own this business concept?

Start here when a requirement names a business noun but does not say which Domainry owner should control it. The important decision is the source of truth, not the screen or table name.

## Problems solved

- Prevents one business concept from being copied into project tables, Runtime metadata, and module state with competing lifecycle and authorization rules.

## Business scenarios

- Deciding whether tenant wording maps to the Runtime Workspace boundary or to an independent business Object.
- Separating login users and organization hierarchy from customer, order, and other project-owned business facts.
- Choosing between one Object-local select and a shared Runtime-authored dictionary that Metadata resolves and localizes.
- Integrating an ERP/CRM master without creating a second ungoverned source of truth.

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
4. If the owner has a native metadata adapter, use it. If the source capability exists but the adapter is incomplete, implement the typed Deck adapter from the locked source schema. If the source contract itself lacks the required shape, add a source-owned metadata extension first, then its Deck adapter. In every case, keep the original business requirement and do not replace it with a lookalike Object.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Employees sign in and also have payroll-specific facts. | Identity user plus a project-owned employee profile | Keep credentials, session, and principal in Identity; relate one employee profile to that Identity user for payroll facts. | Copying password, email login state, Role assignment, or the whole user into an employee Object. |
| Regions and stores place staff and control regional access, while stores also have commercial attributes. | Identity organization units plus an optional project store profile | Identity owns the region/store tree and placement; relate a store profile only for facts such as floor area or sales target. | Maintaining a second parent/child tree in project tables and trying to synchronize it. |
| A SaaS customer is called a tenant and owns customers and orders. | Runtime Workspace plus project customer/order Objects | Translate tenant isolation to Workspace context; model only the independent customer and order business lifecycle. | Creating `tenant`, `workspace`, `tenant_id`, or `workspace_id` project fields. |
| CRM stores customer companies while Identity stores the internal company/department tree. | Project `customer_company` Object plus Identity organizations | Keep commercial status, credit tier, contacts, and addresses in the CRM Object; use Identity organization units only for principal placement and authorization. | Moving every company-shaped noun into Identity or using a CRM parent relation as the authorization tree. |
| One order field has fixed statuses, while several owners share cancellation reasons. | Field-local select for status; Runtime dictionary for shared reasons | Keep status beside the order schema; author one stable shared reason dictionary and let Metadata resolve localized labels. | Creating a dictionary for every dropdown or copying one shared catalog into every Object. |
| ERP owns product master data. | Integration identity/mapping plus project cache or projection with explicit authority | Preserve ERP external identity, sync receipt, and freshness; let project data be a governed projection rather than a competing master. | Editing both systems as peers or dropping external identity after import. |

## Example

Product request: “Each tenant has regions, stores, employees, customer companies, orders, shared cancellation reasons, and products synchronized from ERP.”

- Map “tenant” to the Runtime Workspace boundary; do not author a tenant Object or `tenant_id` field.
- Use Identity organization units for regions and stores when they drive user placement and organization data scope.
- Use Identity users for employees who authenticate.
- Use project Objects for customers and orders because they have independent business facts and lifecycle.
- If store also has domain-specific facts, model a store profile related to the Identity organization instead of copying the organization tree.
- Keep order status as an Object-local select; use `schema.dictionary` only for the genuinely shared cancellation-reason catalog, with Metadata resolving localized labels.
- Keep ERP product ID, synchronization evidence, and freshness on the Integration boundary; do not silently make an editable project copy the second master.

Still confirm whether customer companies ever place users or govern access, whether cancellation reasons truly cross owners, and whether product edits flow back to ERP. Those answers change ownership; page names do not.

## Permissions and scope

Ownership selection happens before Role grants. Authorization cannot repair a duplicated source of truth.

## Boundaries

The exact accepted Model shape is always `model.schema.json`. Capability guides explain decisions but never expand that schema by prose alone; a missing shape is completed through schema, semantic validation, lowering/codegen, authorization, and acceptance tests before it is used.
