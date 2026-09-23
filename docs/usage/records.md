# When should I model a Runtime Object and use CRUD?

Runtime owns ordinary persistence, typed validation, workspace isolation, record identity, ownership, versioning, and CRUD. The project Model declares only business Objects and business fields.

## Problems solved

- Provides governed persistence, validation, relations, closed structured-field semantics, Workspace isolation, and ordinary CRUD without building a project-local data framework.

## Business scenarios

- Maintaining customers, products, contracts, orders, or other durable business records with list, detail, create, and edit experiences.
- Relating one project-owned Object to another while Runtime owns identity, versioning, and audit timestamps.
- Attaching one file or a bounded file list to a business record with field-level MIME, size, count, scan, authorization, export, and lifecycle rules.
- Persisting a canonical set of selected values or a bounded JSON integration snapshot without inventing delimiter conventions or database-specific behavior.
- Enforcing Workspace-scoped composite uniqueness such as “store + external order number” without a check-then-insert race.

## Use when

- Use an Object for a business entity with its own facts and lifecycle.
- Use Runtime CRUD when the operation is ordinary field persistence with no named business command or cross-record rule.

## Do not use when

- Do not declare Runtime-owned fields such as `id`, `version`, `workspace_id`, `owner_user_id`, `owner_org_id`, `created_at`, or `updated_at`.
- Do not use an Object to copy Identity users, organization trees, Inbox items, schedules, or external connections.

## How to use

Declare the smallest Object and field set accepted by `model.schema.json`. Enable only the CRUD/export capabilities the product requires and follow the schema's relation-target contract.

Use the closed `file` type for one attachment and `file_list` for a bounded collection. A file field may declare `allowed_mime_types`, `max_size_bytes`, `max_files` for lists, and `scan_required`. Upload first, wait for a clean scan receipt when required, then save the structured reference containing `file_id`, `filename`, `content_type`, `size`, `content_sha256`, and the clean `scan_receipt`. Never store a download URL or an arbitrary filename in a text field.

Use `multi_select` for an unordered set of stable option values. Declare top-level `options` as `{value,label}` objects and optionally set `config.max_items` (default `100`, maximum `1000`). Runtime trims, deduplicates, sorts, and stores the set as canonical JSON; `max_items` applies to that canonical set after deduplication. `eq` and `ne` compare the complete set; `in` and `not_in` compare the complete set against a list of candidate sets. They do not mean “contains one member”. Null checks are supported; membership, range, text search, sort, unique, and index semantics are intentionally absent. Report Object SQL rejects the type because portable set SQL has not been defined.

Use `json` only for a bounded opaque object or array whose members do not need Runtime filtering. `config.json_shape` is `object` by default and may be `array`; `config.max_json_bytes` defaults to 64 KiB and cannot exceed 1 MiB. Runtime also caps nesting depth and node count, preserves JSON numbers, and emits canonical JSON through CSV/Data Exchange. JSON supports null checks only and is not searchable, sortable, unique, or indexed. Report Object SQL rejects JSON explicitly instead of pretending its meaning is portable across databases.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Staff create, view, edit, and list customer accounts. | Runtime Object CRUD | Declare a `customer` Object with only business fields and enable the required CRUD capabilities; Runtime supplies identity, Workspace, ownership, version, and timestamps. | Building a project repository/controller framework or declaring Runtime system fields. |
| Every sales order belongs to one customer. | Typed project relation | Add a required `relation` field whose `target` is the declared `customer` Object; grant read/write permissions independently. | Storing an unvalidated customer ID string or relating to an undeclared/module-owned table. |
| Every order has queryable line items and one store-local external number. | Order root, line-item Object/Relation, and `composite_unique` validation | Relate each line to one order and declare a Workspace-scoped unique tuple over `store` and `external_order_no`; let Runtime reject the losing concurrent create atomically. | Packing lines into opaque JSON or performing a separate “does it exist?” query before create. |
| A contract has one signed PDF and up to five supporting files. | `file` and `file_list` fields | Declare MIME, size, count, and scan policy; upload under the exact Object/field, then persist Runtime's structured references after scan. | Uploading against a `text` field or storing `/uploads/...` strings and trusting the client. |
| A case has zero or more stable category tags. | `multi_select` field | Declare stable option values and labels plus a bounded `max_items`; compare only canonical complete sets when filtering is required. | Storing comma-separated text, treating `in` as member containment, or adding a database-specific array index. |
| An inbound webhook snapshot must be retained but not queried by members. | `json` field | Choose object/array shape and byte bound; validate business-critical members in a named Operation before saving the canonical value. | Treating opaque JSON as an unbounded schema escape hatch or assuming portable member-path queries and reports. |
| “Activate customer” checks eligibility and changes related records. | Business Operation, not plain CRUD | Keep the customer record as an Object, but expose activation as a named Operation with the required generated capabilities. | Letting the frontend patch `status` directly or hiding the rule in an update hook. |

## Example

Product request: “Maintain orders and queryable order lines; within one Workspace, the same store may not reuse an external order number.”

```json
{
  "schema_version": "domainry.model/v3",
  "objects": [
    {
      "key": "store",
      "name": "Store profile",
      "capabilities": {"create": true, "read": true, "update": true, "delete": false, "export": false},
      "fields": [
        {"key": "code", "name": "Code", "type": "text", "required": true, "unique": true}
      ]
    },
    {
      "key": "sales_order",
      "name": "Sales order",
      "fields": [
        {"key": "store", "name": "Store", "type": "relation", "required": true, "target": "store"},
        {"key": "external_order_no", "name": "External order number", "type": "text", "required": true},
        {"key": "status", "name": "Status", "type": "select", "required": true, "options": [{"value": "draft", "label": "Draft"}, {"value": "submitted", "label": "Submitted"}], "default": "draft"}
      ],
      "validations": [{"key": "store_external_order", "type": "composite_unique", "fields": ["store", "external_order_no"]}]
    },
    {
      "key": "sales_order_line",
      "name": "Sales order line",
      "fields": [
        {"key": "order", "name": "Order", "type": "relation", "required": true, "target": "sales_order"},
        {"key": "sku", "name": "SKU", "type": "text", "required": true},
        {"key": "quantity", "name": "Quantity", "type": "number", "required": true}
      ]
    }
  ]
}
```

Creating `SO-88` for `store-shanghai-01` succeeds once. A concurrent second create with the same tuple fails as a deterministic uniqueness conflict and leaves no partial order or line. Deleting an order must first obey the declared Relation deletion rule; queryable line items are not hidden inside JSON.

## Permissions and scope

Every enabled Object capability creates a distinct permission such as `customer.read` or `sales_order.update`. A Role must grant each exact permission with one data scope.

## Boundaries

Object JSON must not absorb state machines, Identity relationships, analytical definitions, schedules, or other owners' contracts. Match those requirements to their source owner first.

File fields are intentionally not text-searchable, value-filterable, sortable, unique, or indexed. Null checks are supported; CSV export emits canonical JSON. Downloads resolve immutable `file_id` under the record's Workspace/Object/Field authorization and fail closed unless scan evidence is clean.

`multi_select` is a canonical set rather than an ordered list; its only value comparisons are exact-set `eq`/`ne` and candidate-set `in`/`not_in`. `json` is an opaque bounded value with null checks only. Neither type may be unique, indexed, text-searched, sorted, or selected by Report Object SQL. Changing any active field from or to a different type is rejected by schema upgrade planning, including physical-storage-compatible changes such as `text` to `multi_select`.
