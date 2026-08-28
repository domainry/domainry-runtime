package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import "context"

func (s *ChangePlanReferenceApplicationService) addSeedProvenanceReferences(ctx context.Context, builder *changeplanprojection.ChangePlanReferenceGraphBuilder) error {
	if s.evidence == nil {
		return nil
	}
	seeds, err := s.evidence.ListSeedProvenance(ctx)
	if err != nil {
		return referenceInternalError("list domain seed provenance", err)
	}
	for _, seed := range seeds {
		key := seed.ObjectKey + "." + seed.SeedKey
		builder.Node("seed_record", key, seed.ObjectKey, seed.SeedKey, seed.SourceKind)
		builder.Edge("seed_record", key, "object", seed.ObjectKey, "materialized_from_seed", "object_key")
		recordKey := seed.ObjectKey + "." + seed.RecordID
		builder.Node("record", recordKey, seed.ObjectKey, seed.RecordID, "runtime")
		builder.Edge("seed_record", key, "record", recordKey, "identifies_record", "record_id")
	}
	return nil
}
