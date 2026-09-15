package integrationtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
)

func verifyProfessionalExecutionSharing(t *testing.T, issuer, receiver *businessBrowser, detail agent.ConversationDelegationDetail, execution agent.ConversationRun, refs []agent.ConversationResultReference) func(int) {
	t.Helper()
	return verifyProfessionalExecutionSharingSteps(t, issuer, receiver, detail, execution, refs, 7)
}

func verifyProfessionalExecutionSharingSteps(t *testing.T, issuer, receiver *businessBrowser, detail agent.ConversationDelegationDetail, execution agent.ConversationRun, refs []agent.ConversationResultReference, expectedSteps int) func(int) {
	t.Helper()
	path := "/agent/delegations/" + detail.ID
	ref := agent.ConversationRunReference{ConversationID: detail.ConversationID, RunID: execution.ID}
	var current agent.ConversationDelegationDetail
	if err := json.Unmarshal(receiver.call("GET", path, nil, 200).Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	var publication agent.ConversationExecutionPublication
	if err := json.Unmarshal(receiver.call("POST", path+"/execution-publications", agent.ConversationExecutionShare{Reference: ref, ClientID: "professional-execution-share", ExpectedRevision: current.Revision, Reason: "实际执行用户共享原专业执行过程"}, 200).Body.Bytes(), &publication); err != nil {
		t.Fatal(err)
	}
	if publication.Reference != ref || publication.Publisher.UserID != detail.ExecutionSubject.UserID {
		t.Fatal("professional publication lost actual user", publication)
	}
	read := func(want int) {
		t.Helper()
		response := issuer.call("POST", path+"/execution", ref, want)
		if want != 200 {
			return
		}
		var run agent.ConversationRun
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		// Each deterministic model declares its complete step count. An ordinary
		// private projection under a different role is not the full-run oracle.
		if run.ID != execution.ID || run.ConversationID != execution.ConversationID || run.AccessError != "" || len(run.Steps) != expectedSteps || run.Interaction != nil || run.WriteScope != nil {
			t.Fatalf("shared professional run id=%s status=%s steps=%d ordinary_steps=%d error=%s", run.ID, run.Status, len(run.Steps), len(execution.Steps), run.AccessError)
		}
		multipage := false
		for _, resultRef := range refs {
			raw := []byte{}
			pages := 0
			for offset := 0; ; {
				var page agent.ConversationResultSlice
				if err := json.Unmarshal(issuer.call("POST", path+"/execution-result", agent.ConversationResultRead{Reference: resultRef, Offset: offset, MaxBytes: 256}, 200).Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Reference != resultRef || page.Offset != offset || page.NextOffset < offset || page.NextOffset == offset && !page.Complete {
					t.Fatal("professional exact page changed", page)
				}
				raw = append(raw, []byte(page.JSONText)...)
				pages++
				if pages > 1024 {
					t.Fatal("professional pagination did not terminate")
				}
				if page.Complete {
					break
				}
				offset = page.NextOffset
			}
			digest := sha256.Sum256(raw)
			if hex.EncodeToString(digest[:]) != resultRef.SHA256 {
				t.Fatal("professional original digest changed", resultRef)
			}
			t.Logf("Exact professional execution result %s: %d pages, %d bytes, original SHA-256 verified", resultRef.CallID, pages, len(raw))
			multipage = multipage || pages > 1
		}
		if !multipage {
			t.Fatal("professional results did not exercise nonterminal pages")
		}
	}
	read(200)
	return read
}
