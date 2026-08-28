package policy

import (
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func validRetentionPolicy() lifecyclemodel.RetentionPolicy {
	return lifecyclemodel.RetentionPolicy{
		Key: "record.default", Version: "1", Owner: "record",
		Class:            lifecyclemodel.RetentionClassProduct,
		Sensitivity:      []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityInternal},
		DefaultRetention: 30 * 24 * time.Hour, MinimumRetention: 24 * time.Hour,
		StatusRetention: map[string]time.Duration{"closed": 7 * 24 * time.Hour},
		ReplayWindow:    12 * time.Hour, WorkspaceMayExtend: true, WorkspaceMayReduce: true,
		BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete,
	}
}

func TestValidateRetentionPolicyMatrix(t *testing.T) {
	base := validRetentionPolicy()
	if err := ValidateRetentionPolicy(base); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name   string
		mutate func(*lifecyclemodel.RetentionPolicy)
	}{
		{name: "key", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.Key = " " }},
		{name: "version", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.Version = "" }},
		{name: "owner", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.Owner = "" }},
		{name: "class", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.Class = "unknown" }},
		{name: "default duration", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.DefaultRetention = 0 }},
		{name: "minimum duration", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.MinimumRetention = -1 }},
		{name: "default below minimum", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.DefaultRetention = time.Hour }},
		{name: "default below replay", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.DefaultRetention = 6 * time.Hour
			policy.MinimumRetention = 6 * time.Hour
		}},
		{name: "minimum below replay", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.MinimumRetention = 6 * time.Hour }},
		{name: "empty status", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.StatusRetention = map[string]time.Duration{" ": 24 * time.Hour}
		}},
		{name: "status below minimum", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.StatusRetention = map[string]time.Duration{"closed": time.Hour}
		}},
		{name: "legal reduction", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.Class = lifecyclemodel.RetentionClassLegalAudit }},
		{name: "audit reduction", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.Sensitivity = []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}
		}},
		{name: "security reduction", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.Sensitivity = []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}
		}},
		{name: "user erase ineligible", mutate: func(policy *lifecyclemodel.RetentionPolicy) {
			policy.Class = lifecyclemodel.RetentionClassUserErase
			policy.EraseBehavior = lifecyclemodel.EraseBehaviorNotEligible
		}},
		{name: "backup behavior", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.BackupBehavior = "" }},
		{name: "erase behavior", mutate: func(policy *lifecyclemodel.RetentionPolicy) { policy.EraseBehavior = "" }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			policy := base
			test.mutate(&policy)
			if err := ValidateRetentionPolicy(policy); err == nil {
				t.Fatal("expected invalid retention policy")
			}
		})
	}
	for _, class := range []lifecyclemodel.RetentionClass{
		lifecyclemodel.RetentionClassProduct,
		lifecyclemodel.RetentionClassLegalAudit,
		lifecyclemodel.RetentionClassTechnical,
		lifecyclemodel.RetentionClassUserErase,
	} {
		policy := base
		policy.Class = class
		policy.WorkspaceMayReduce = false
		if err := ValidateRetentionPolicy(policy); err != nil {
			t.Fatalf("valid class %q: %v", class, err)
		}
	}
	noReplay := base
	noReplay.ReplayWindow = 0
	if err := ValidateRetentionPolicy(noReplay); err != nil {
		t.Fatalf("zero replay window: %v", err)
	}
}

func TestEffectiveWorkspaceRetentionMatrix(t *testing.T) {
	base := validRetentionPolicy()
	invalidPolicy := base
	invalidPolicy.Key = ""
	if _, err := EffectiveWorkspaceRetention(invalidPolicy, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace", Retention: 30 * 24 * time.Hour}); err == nil {
		t.Fatal("invalid policy must fail")
	}
	for _, override := range []lifecyclemodel.WorkspaceRetentionOverride{{Retention: 30 * 24 * time.Hour}, {WorkspaceID: "workspace"}, {WorkspaceID: "workspace", Retention: -1}} {
		if _, err := EffectiveWorkspaceRetention(base, override); err == nil {
			t.Fatalf("invalid override %+v succeeded", override)
		}
	}
	noReduce := base
	noReduce.WorkspaceMayReduce = false
	if _, err := EffectiveWorkspaceRetention(noReduce, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace", Retention: 20 * 24 * time.Hour}); err == nil {
		t.Fatal("unauthorized reduction must fail")
	}
	noExtend := base
	noExtend.WorkspaceMayExtend = false
	if _, err := EffectiveWorkspaceRetention(noExtend, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace", Retention: 40 * 24 * time.Hour}); err == nil {
		t.Fatal("unauthorized extension must fail")
	}
	if _, err := EffectiveWorkspaceRetention(base, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace", Retention: 12 * time.Hour}); err == nil {
		t.Fatal("retention below minimum must fail")
	}
	for _, retention := range []time.Duration{24 * time.Hour, 30 * 24 * time.Hour, 40 * 24 * time.Hour} {
		got, err := EffectiveWorkspaceRetention(base, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace", Retention: retention})
		if err != nil || got != retention {
			t.Fatalf("effective retention = %v/%v, want %v", got, err, retention)
		}
	}
	if !containsSensitivity([]lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityInternal, lifecyclemodel.SensitivityPII}, lifecyclemodel.SensitivityPII) || containsSensitivity([]lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityInternal}, lifecyclemodel.SensitivityAudit) {
		t.Fatal("sensitivity membership mismatch")
	}
}
