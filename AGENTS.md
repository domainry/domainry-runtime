# Development rules

- Answer architecture and implementation questions from the current repository code and tests, not from memory.
- Every database has exactly one host-owned migration ledger named `_schema_migrations`. Embedded modules must submit source-owned migrations through the host migration registrar and must not create module-prefixed ledgers.
- Persistence DDL and DML must use `github.com/domainry/domainry-orm`. Raw SQL is allowed only when the ORM has no equivalent, with a local justification and dialect-focused tests.
- Embedded modules use the host database, transaction boundary, SQL dialect, migration lock, and migration ledger. Business ownership remains in the source module.
