package capability

import (
	"context"
	"net/url"
	"sort"
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *CapabilityAuthoringApplicationService) DirectAuthoringSuccessProjection(ctx context.Context, capabilityKey string, principal principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
	contract, err := s.Capabilities(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{}, err
	}
	return directAuthoringSuccessProjection(contract, capabilityKey)
}

func directAuthoringSuccessProjection(contract capabilitycontract.CapabilityRuntimeAuthoringContract, capabilityKey string) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
	capabilityKey = strings.TrimSpace(capabilityKey)
	foundCapability := false
	outputTypes := map[string]bool{}
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			if definition.Key == capabilityKey {
				foundCapability = true
				for _, output := range definition.OutputVariables {
					if output.VisibleTo == "subsequent_capability_calls" && strings.TrimSpace(output.Type) != "" {
						outputTypes[strings.TrimSpace(output.Type)] = true
					}
				}
			}
		}
	}
	if !foundCapability {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{}, capabilityDiscoveryNotFound("backend.capability.not_found", "capability", capabilityKey)
	}
	successors := []capabilitycontract.CapabilityAuthoringSuccessorSummary{}
	seen := map[string]bool{}
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			if definition.Status != "supported" || (!capabilityStringContains(definition.Requires, capabilityKey) && !capabilityReferencesOutputType(definition, outputTypes)) || seen[definition.Key] {
				continue
			}
			seen[definition.Key] = true
			successors = append(successors, capabilitycontract.CapabilityAuthoringSuccessorSummary{
				Key: definition.Key, Domain: domain.Key, Status: definition.Status,
				DetailEndpoint:     "/capabilities/" + url.PathEscape(definition.Key),
				ValidationEndpoint: definition.ValidationEndpoint,
			})
		}
	}
	sort.Slice(successors, func(left, right int) bool { return successors[left].Key < successors[right].Key })
	return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: contract.InstanceHash, AvailableSuccessors: successors}, nil
}

func capabilityReferencesOutputType(definition capabilitycontract.CapabilityAuthoringDefinition, outputTypes map[string]bool) bool {
	for _, reference := range definition.ReferenceContracts {
		if outputTypes[strings.TrimSpace(reference.Kind)] {
			return true
		}
	}
	return false
}
