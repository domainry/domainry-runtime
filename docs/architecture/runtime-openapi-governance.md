# Runtime OpenAPI Governance

Runtime uses **code-first OpenAPI generation**. Route registration and typed request/response contracts in Go are the source of truth; generated OpenAPI is a derived inspection artifact and is never maintained as a parallel static source.

Changes to handlers, status codes or payloads must update the code contract and its tests together. CI verifies the generated document through the Runtime endpoint and prevents committed static OpenAPI sources from becoming a second authority.
