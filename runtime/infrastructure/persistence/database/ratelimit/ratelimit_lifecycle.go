package ratelimit

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "runtime_security", lifecyclepersistence.RelationalCleanupSpec{
		PolicyKey: "ratelimit.bucket.v1", Table: "runtime_rate_limit_bucket", IDColumn: "bucket_key", TimeColumn: "updated_at_ns", UnixNanoTime: true,
	})
}
