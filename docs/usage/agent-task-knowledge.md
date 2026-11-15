# Library-aware Knowledge reads for Agent Tasks

Runtime's Agent Task host supports the SDK's explicit `knowledge_search` and
`knowledge_read` tools. It uses the prepared, independent Knowledge owner and
never obtains retrieval authority through project Object permissions or an
Agent-owned copy of the Knowledge service.

Every call follows Task credential validation, live execution-principal and
Agent/Skill authorization, the existing rate/input/output/timeout budgets, and
then Knowledge's current library/document access policy. Search arguments are
only `library_id` and `query`; read arguments are only `library_id` and
`document_id`. Extra fields, authority/permission injection and malformed
arguments are rejected before invoking Knowledge.

After retrieval, the host verifies the requested library, operation, query or
document, scoped receipt and citation identifiers, and revalidates the entire
owner result. Only normalized citations and source receipt metadata are
returned. The upstream `Data` payload is excluded. A missing source, denied or
revoked grant, invalid result, oversized output or timeout remains a failure.
An authorized no-match search remains an explicit empty citation collection.

Knowledge must be installed through its public Module/Remote boundary and must
provide library-aware retrieval. Default unscoped search is never a fallback.
The principal schema keeps these two explicitly declared tools visible to authenticated identities even when the Task declares no project Objects. They do not require an unrelated `integration.tool.*` grant: the live Knowledge library/document check remains the resource authority. Existing Agents do not gain these tools automatically. Services must have the
specific Knowledge resource grants needed for their actual workload.

The Agent owner continues to own the Task tool-call ledger, lease fencing,
current Task budget and failed terminal receipts. Product code must retain
actual retrieval references and versions; model-authored citations are not
permission grants. Display or later customer release needs the current
Knowledge authorization and product audience policy.

## Trusted completion references

The Knowledge adapter also produces a separate typed `AgentTaskKnowledgeEvidence` from its already reauthorized result. The Task host passes that receipt to Agent outside model-visible output. Agent SDK v0.1.59 / Agent v0.1.100 retain it in the same fenced tool call; raw model JSON cannot populate it.

The Workflow completion snapshot projects the receipt on `WorkflowAgentToolEvidence.Knowledge`, under the existing exact Workflow/Process/record/actor grant. Runtimeext v64 includes the reference schema in its contract digest. Only executed Knowledge search/fetch tools with a valid, bounded receipt can project references. Replayed completion callbacks cannot replace the saved document/version references. A later authorized Action read rejects malformed or wrongly attached receipts.

References contain library, citation/document/page/chunk/record identities, versions, content/source revision hashes and scope. They contain no title, excerpt, URL, provider raw data, credentials or permission bundle. No-match searches retain an empty collection. This is evidence of a prior authorized tool result, never a durable grant to read that source; current Knowledge and product audience authorization still govern later display or customer release.
