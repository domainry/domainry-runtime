# When should I model a Runtime Object and use CRUD?

Runtime owns ordinary persistence, typed validation, workspace isolation, record identity, ownership, versioning, and CRUD. The project Model declares only business Objects and business fields.

## Problems solved

- Provides governed persistence, validation, relations, Workspace isolation, and ordinary CRUD without building a project-local data framework.

## Business scenarios

- Maintaining customers, products, contracts, orders, or other durable business records with list, detail, create, and edit experiences.
- Relating one project-owned Object to another while Runtime owns identity, versioning, and audit timestamps.

## Use when

- Use an Object for a business entity with its own facts and lifecycle.
- Use Runtime CRUD when the operation is ordinary field persistence with no named business command or cross-record rule.

## Do not use when

- Do not declare Runtime-owned fields such as `id`, `version`, `workspace_id`, `owner_user_id`, `owner_org_id`, `created_at`, or `updated_at`.
- Do not use an Object to copy Identity users, organization trees, Inbox items, schedules, or external connections.

## How to use

Declare the smallest Object and field set accepted by `model.schema.json`. Enable only the CRUD/export capabilities the product requires and follow the schema's relation-target contract.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Staff create, view, edit, and list customer accounts. | Runtime Object CRUD | Declare a `customer` Object with only business fields and enable the required CRUD capabilities; Runtime supplies identity, Workspace, ownership, version, and timestamps. | Building a project repository/controller framework or declaring Runtime system fields. |
| Every sales order belongs to one customer. | Typed project relation | Add a required `relation` field whose `target` is the declared `customer` Object; grant read/write permissions independently. | Storing an unvalidated customer ID string or relating to an undeclared/module-owned table. |
| “Activate customer” checks eligibility and changes related records. | Business Operation, not plain CRUD | Keep the customer record as an Object, but expose activation as a named Operation with the required generated capabilities. | Letting the frontend patch `status` directly or hiding the rule in an update hook. |

## Example

Product request: “Maintain customer accounts and relate each order to one customer.”

```json
{
  "schema_version": "domainry.model/v3",
  "objects": [
    {
      "key": "customer",
      "name": "Customer",
      "capabilities": {"create": true, "read": true, "update": true, "delete": false, "export": false},
      "fields": [
        {"key": "business_no", "name": "Business number", "type": "text", "required": true, "unique": true},
        {"key": "status", "name": "Status", "type": "select", "required": true, "options": ["active", "suspended"], "default": "active"}
      ]
    },
    {
      "key": "sales_order",
      "name": "Sales order",
      "fields": [
        {"key": "customer", "name": "Customer", "type": "relation", "required": true, "target": "customer"},
        {"key": "total", "name": "Total", "type": "number", "required": true}
      ]
    }
  ]
}
```

Use Runtime CRUD for an ordinary customer edit. If “activate customer” has eligibility rules or coordinated writes, define a Business Operation instead.

## Permissions and scope

Every enabled Object capability creates a distinct permission such as `customer.read` or `sales_order.update`. A Role must grant each exact permission with one data scope.

## Boundaries

Object JSON must not absorb state machines, Identity relationships, analytical definitions, schedules, or other owners' contracts. Match those requirements to their source owner first.
