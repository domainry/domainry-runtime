package action

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRuntimeextPrincipalCarriesDetachedActiveBusinessProfile(t *testing.T) {
	internal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		UserID: "user-1"},
		ActiveBusinessProfile: &profilebindingmodel.Reference{
			BindingKey: "member",
			ObjectKey:  "member",
			RecordID:   "member-1",
		},
	}, accessfixture.Bundle{Key: "member"},
	)
	public := toRuntimeextPrincipal(internal)
	if public.ActiveBusinessProfile == nil ||
		public.ActiveBusinessProfile.BindingKey != "member" ||
		public.ActiveBusinessProfile.ObjectKey != "member" ||
		public.ActiveBusinessProfile.RecordID != "member-1" {
		t.Fatalf("public Principal lost active Business Profile: %+v", public)
	}
	public.ActiveBusinessProfile.RecordID = "mutated-by-handler"
	if internal.ActiveBusinessProfile.RecordID != "member-1" {
		t.Fatalf("project-facing Principal aliases Runtime identity state: internal=%+v public=%+v", internal.ActiveBusinessProfile, public.ActiveBusinessProfile)
	}
	execution := &businessActionExecution{principal: toRuntimeextPrincipal(internal)}
	first := execution.Principal()
	first.ActiveBusinessProfile.RecordID = "mutated-by-generated-binding"
	if second := execution.Principal(); second.ActiveBusinessProfile == nil || second.ActiveBusinessProfile.RecordID != "member-1" {
		t.Fatalf("ActionExecution Principal getter leaked mutable identity state: first=%+v second=%+v", first, second)
	}
}
