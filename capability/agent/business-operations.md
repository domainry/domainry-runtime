# When does behavior need a Business Operation and Handler?

A Business Operation is a named command implemented by one generated Handler. Runtime owns transport, authentication, authorization, idempotency, transaction control, and persistence capabilities.

## Problems solved

- Gives named business commands one atomic, least-privilege boundary for validation, coordinated writes, concurrency control, and stable refusals.

## Business scenarios

- Activating an account only from an eligible state and returning a stable conflict when the record changed concurrently.
- Issuing an invoice, reserving inventory, or submitting a case when several records must succeed or roll back together.

## Use when

- Use a Business Operation when plain CRUD cannot truthfully express the command.
- Declare only the Object capabilities the Handler algorithm actually needs.

## Do not use when

- Do not wrap ordinary create/update/delete merely to rename it.
- Do not open SQL, a database handle, a generic Runtime client, or direct network access from the Handler.

## How to use

Choose `object`, `record`, or `bulk` scope. Declare typed input/output, stable business errors, and the least-privilege capability operations. Use `get_for_update` or `conditional_update` for state-dependent writes.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Activate an account only when it is pending. | Record-scoped Business Operation | Declare `account.activate`, request `get_for_update` or `conditional_update`, publish stable refusal codes, and grant the exact Operation permission. | Exposing unrestricted status update or checking the state only in the browser. |
| Issue an invoice and persist its issuance receipt together. | One atomic Handler with both Object capabilities | Read the invoice under concurrency control, update it, create the receipt, and return an error so Runtime rolls back both on failure. | Two HTTP calls, direct SQL, or committing the invoice before receipt creation. |
| Rename ordinary customer update to “save customer”. | Runtime CRUD | Keep ordinary field persistence on the generated update capability. | Creating a Business Operation that adds no business rule, transaction, or stable command meaning. |

## Example

Product request: “Activate an account only when it is pending, and return a stable conflict when another actor changed it.”

```json
{
  "schema_version": "domainry.model/v3",
  "operations": [
    {
      "key": "account.activate",
      "name": "Activate account",
      "object_key": "account",
      "owner": "business",
      "scope": "record",
      "output": [{"key": "status", "name": "Status", "type": "select", "options": ["active"]}],
      "errors": [{"code": "account.activation_conflict", "message": "Account changed before activation"}],
      "capabilities": [
        {"object_key": "account", "operations": ["get_for_update", "conditional_update"]}
      ]
    }
  ]
}
```

The Handler reads the routed record under lock, checks the business precondition, performs the conditional update, and returns errors unchanged so Runtime rolls back the complete command.

## Permissions and scope

Grant the exact `account.activate` permission to allowed Roles. The Operation’s data scope controls its generated Object access; Object CRUD permissions do not imply the named command.

## Boundaries

Waiting, approval tasks, recurrence, analytical reports, notification policy, and provider protocol belong to their own owners. If the required typed capability is absent, change the contract or report a Contract Gap.
