package workspaceprovision

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	application "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	model "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	signature "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func TestSignedProvisionReusesAtomicStoreReceiptsAndRollback(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	initial := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}, probe.rolePolicy)
	if _, err := initial.Initialize(t.Context(), validWorkspaceRequest("bootstrap")); err != nil {
		t.Fatal(err)
	}
	repository := initial
	probe.claimed = false // The fixture models one outstanding credential at a time.
	service := application.NewWorkspaceProvisionApplicationService(repository)
	request := validWorkspaceRequest("signed-create")
	request.WorkspaceCode = "signed-shop"
	invoke := func(input model.Request) (model.Result, error) {
		body, _ := json.Marshal(input)
		now := time.Now()
		stamp := strconv.FormatInt(now.Unix(), 10)
		secret := []byte("workspace-provision-dedicated-secret")
		meta := signature.SignedRequest{Method: "POST", Path: "/workspaces", RuntimeID: "runtime-a", IdempotencyKey: input.RequestID}
		sig, err := signature.SignRequest(body, meta, "operations", stamp, secret)
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := application.VerifySignedProvision(t.Context(), body, meta, signature.Signature{Version: signature.CallbackSignatureContractVersion, ClientID: "operations", Timestamp: stamp, Value: sig}, "operations", secret, now)
		if err != nil {
			t.Fatal(err)
		}
		return service.Provision(ctx, principalmodel.Principal{}, input)
	}
	created, err := invoke(request)
	if err != nil || created.InitialPassword == "" || created.Replayed {
		t.Fatalf("creation err=%v replay=%v credential=%v", err, created.Replayed, created.InitialPassword != "")
	}
	replayed, err := invoke(request)
	if err != nil || !replayed.Replayed || replayed.InitialPassword != "" || replayed.WorkspaceID != created.WorkspaceID {
		t.Fatalf("replay err=%v replay=%v credential=%v", err, replayed.Replayed, replayed.InitialPassword != "")
	}
	request.WorkspaceName = "Changed"
	if _, err := invoke(request); apperror.CodeOf(err) != "workspace.provision_idempotency_conflict" {
		t.Fatalf("conflict=%v", err)
	}
	assertRowCount(t, store, "_workspaces", 2)
	assertRowCount(t, store, workspaceProvisioningReceiptTable, 2)

	repository.failures = NewAcceptanceFailureInjector(FailureAfterWorkspaceConfiguration)
	request.RequestID, request.WorkspaceCode = "signed-rollback", "rolled-back"
	if _, err := invoke(request); !errors.Is(err, model.ErrAcceptanceFailure) {
		t.Fatalf("rollback=%v", err)
	}
	assertRowCount(t, store, "_workspaces", 2)
	assertRowCount(t, store, "_workspace_commercial_configuration", 2)
	assertRowCount(t, store, workspaceProvisioningReceiptTable, 2)
}
