# Action-scoped ingestion of external files

External customer bytes must enter the normal Runtime upload scan queue. They
must not use `CreateDerivedFile`, which attests internally generated content.
The additive `runtimeext-v65` contract provides `IngestUploadedFile` and
`InspectUploadedFile`; no generic HTTP route, database table or extra file store
is introduced.

## Authority and field policy

A source-owned Business Handler declares `FileOperationIngestUpload` and a
create/update capability for the exact target Object. Inspection requires
`FileOperationInspectUpload` and a read capability for that Object. The field
must be a declared `file` or `file_list` with scanning required. A grant for
the semantic Action is sufficient; ordinary direct Object create/update
permissions are not needed and are not added by this feature.

Runtime supplies Workspace, uploader and Action execution identity. Missing or
mismatched Workspace identity is rejected; there is no default or fallback.
The two strict-denial conditions in `uploaded_file_capability_service.go` are
explicitly reviewed in the workspace-fallback candidate inventory. That
inventory also flags lines where another empty identifier is tested alongside
the exact Workspace comparison; both conditions return a forbidden error.

Object field policies keep the 5 MiB default. An explicit `max_size_bytes` may
raise a particular field's limit, bounded at 128 MiB; for example Vesper can
declare 25 MiB. Generic multipart upload also respects that declared limit,
while its existing CRUD authorization remains unchanged.

## ProjectHTTP byte boundary

Parse bounded multipart input at the project HTTP boundary. Derive the actual
size and SHA-256 there, include filename/type/hash/size in the serialized typed
Action input, and keep its stream field tagged `json:"-"`. Invoke the existing
`runtimeengine.Engine.InvokeAction` with this concrete typed input. Runtime's
native input path retains the stream only for this synchronous call. It is
absent from the persisted payload, audit body and idempotency fingerprint;
the actual content identity remains part of that fingerprint through metadata.
A generic JSON Action invocation cannot create the process-local stream.

The binding calls `runtimeext.IngestUploadedFile` with a stable attachment key
and hard-coded target Object/field. Runtime additionally namespaces that key by
the current Action execution and uploader. The staged bytes must exactly match
the supplied size and SHA-256 before any artifact is registered. Different
content under one identity conflicts; a retry cannot reset clean or quarantined
evidence. No filesystem path or caller-supplied Workspace is accepted.

Successful ingestion normally returns `pending` with no scan receipt. The
existing Lifecycle pending queue, configured BlobStore, FileScanner and
FileScanProcessor remain the only source of a clean/quarantined/failed verdict.
Scanner outages keep the file pending for the normal retry path. Inspection
requires the exact uploader, Workspace, Object and field before it returns any
evidence. A clean inspection includes the canonical identity and signed scan
receipt needed by existing Runtime Record file validation.

Ingestion and the eventual business-message mutation are separate steps. A
pending, failed or quarantined file must never be promoted into a clean Record
reference. Unused uploads remain subject to the existing Lifecycle policy.

## Customer BFF boundary

Runtime authorizes the Integration service identity. A product that uses one
service account for many external visitors must additionally bind its own
upload token to the current visitor, conversation and message. The uploader
check alone does not distinguish those visitors. The business Action must verify
that binding and the current clean evidence before attaching the file.

Downloads still require a caller-readable exact Record/field binding and clean
evidence through `IssueFileDownload`; inspection does not grant byte access.
The product BFF verifies the customer session or Case context and proxies the
protected download without exposing its Integration credential to the browser.

## Verification

Tests cover native stream exclusion from JSON, exact Action/Object grants,
pending-before-scan, clean Record references, EICAR/executable quarantine,
transient scanner failure, immutable retry, forged hash/size, MIME/size policy,
and cross-uploader/Workspace/field inspection denial. The explicit field limit
is tested above the unchanged 5 MiB default. These framework checks do not claim
that any downstream product's customer attachment journey is already complete.
