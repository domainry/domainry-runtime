package runtime

import (
	"context"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

var runtimeWorkerOwner = "runtime"

func runtimeWorkerIdentity(name string) workerplatform.Identity {
	return workerplatform.Identity{Owner: runtimeWorkerOwner, Name: name}
}

var runtimeWorkerApplications = func(records *composition.RuntimeServices) composition.RuntimeApplications {
	return records.Applications()
}

// StartWorkers starts every process-owned background worker.
func StartWorkers(ctx context.Context, runtime *Runtime) {
	if !runtime.beginWorkerStartup() {
		return
	}
	// Release admission is process-critical and must share the same long-lived
	// lifecycle context as every other process-owned worker. Starting it during
	// Runtime construction can bind the lease to a shorter activation context.
	runtime.startRuntimeReleaseHeartbeat(ctx)
	runtime.startSchedulerWorker(ctx)
	runtime.startRecordTimerWorker(ctx)
	runtime.StartWorkflowWorker(ctx)
	runtime.StartIntegrationEventWorker(ctx)
	runtime.StartPublicationHandoffWorker(ctx)
	runtime.startConnectorProviderBackgroundWorker(ctx)
	runtime.startIntegrationInvocationReconciliationWorker(ctx)
	runtime.startIntegrationCredentialExpiryWorker(ctx)
	runtime.startIntegrationHealthAlertWorker(ctx)
	runtime.StartNotificationPublicationWorker(ctx)
	runtime.startNotificationInboxWorker(ctx)
	runtime.startNotificationChannelWorker(ctx)
	runtime.startDataExchangeWorker(ctx)
	runtime.startFileScanWorker(ctx)
	runtime.startIdempotencyCleanupWorker(ctx)
	runtime.startArtifactCleanupWorker(ctx)
	runtime.startLifecycleCleanupWorker(ctx)
	runtime.startAccountErasureWorker(ctx)
	if runtime.api != nil {
		runtime.api.MarkStartupComplete()
	}
}

func (a *Runtime) startRecordTimerWorker(ctx context.Context) {
	if a == nil || a.records == nil || runtimeWorkerApplications(a.records).RecordTimers == nil {
		return
	}
	service := runtimeWorkerApplications(a.records).RecordTimers
	config := service.WorkerConfig()
	if !config.Enabled {
		return
	}
	a.startControlledWorker(ctx, "record_timer", func(workerCtx context.Context) <-chan struct{} {
		interval := config.PollInterval
		if interval <= 0 {
			interval = time.Second
		}
		maxInterval := interval * 30
		if maxInterval < 30*time.Second {
			maxInterval = 30 * time.Second
		}
		batch := config.BatchSize
		if batch <= 0 {
			batch = 25
		}
		return workerplatform.StartAdaptiveLoop(workerCtx, runtimeWorkerIdentity("record_timer"), interval, maxInterval, func(tickCtx context.Context) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				processed, err := service.ProcessDueForAllWorkspaces(
					operationCtx,
					time.Time{},
					batch,
					principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "process due record timers"),
				)
				return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
			})
		})
	})
}

func (a *Runtime) startSchedulerWorker(ctx context.Context) {
	if a == nil || a.schedulerBinding == nil {
		return
	}
	a.startControlledWorker(ctx, "scheduler", func(workerCtx context.Context) <-chan struct{} {
		return a.schedulerBinding.Start(workerCtx, schedulersdk.WorkerConfig{Enabled: a.cfg.SchedulerEnabled, PollInterval: a.cfg.SchedulerPollInterval, BatchSize: a.cfg.SchedulerBatchSize, LeaseTTL: a.cfg.SchedulerLeaseTTL})
	})
}

func (a *Runtime) startNotificationChannelWorker(ctx context.Context) {
	if a == nil || a.notificationWorkers == nil {
		return
	}
	a.startControlledWorker(ctx, "notification_channel", func(workerCtx context.Context) <-chan struct{} {
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 100 {
			batch = 25
		}
		wakeups := a.store.WorkerWakeups().Subscribe("notification_channel", 64)
		return workerplatform.StartWakeableRecoveryLoop(workerCtx, runtimeWorkerIdentity("notification_channel"), notificationChannelRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func(tickCtx context.Context) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				processed, err := a.notificationWorkers.ProcessDueChannelPlans(operationCtx, batch)
				return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
			})
		}, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(executeCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				worked, err := a.notificationWorkers.ProcessChannelPlan(operationCtx, notificationSDKWorkLocator(locator))
				return workerplatform.TickResult{Worked: worked, ProcessedCount: boolCount(worked)}, err
			})
		})
	})
}

func notificationChannelRecoveryInterval(interval time.Duration) time.Duration {
	if interval < 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func (a *Runtime) beginWorkerStartup() bool {
	if a == nil {
		return false
	}
	a.workersMu.Lock()
	defer a.workersMu.Unlock()
	if a.workersStarted || a.workersClosing {
		return false
	}
	a.workersStarted = true
	return true
}

func (a *Runtime) startDataExchangeWorker(ctx context.Context) {
	if a == nil || a.dataExchangeBinding == nil || a.records == nil || a.records.Applications().Records == nil {
		return
	}
	a.startControlledWorker(ctx, "data_exchange", func(workerCtx context.Context) <-chan struct{} {
		interval := a.cfg.EffectiveWorkerPollInterval()
		if interval <= 0 {
			interval = time.Second
		}
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 25 {
			batch = 10
		}
		return a.records.Applications().Records.StartDataExchangeWorker(workerCtx, interval, batch)
	})
}

func (a *Runtime) startIdempotencyCleanupWorker(ctx context.Context) {
	if a == nil || a.records == nil || a.records.Applications().RuntimeStatus == nil {
		return
	}
	a.startControlledWorker(ctx, "idempotency_cleanup", func(workerCtx context.Context) <-chan struct{} {
		return a.records.Applications().RuntimeStatus.StartIdempotencyCleanupWorker(workerCtx, 5*time.Minute, 500)
	})
}

func (a *Runtime) StartWorkflowWorker(ctx context.Context) {
	if a == nil || !a.schemaCapabilities.Workflow || a.records == nil || runtimeWorkerApplications(a.records).Workflows == nil {
		return
	}
	a.startControlledWorker(ctx, "workflow", func(workerCtx context.Context) <-chan struct{} {
		return startWorkflowContinuationWorkerLoop(
			workerCtx,
			a.cfg.EffectiveWorkerPollInterval(),
			a.cfg.EffectiveWorkerBatchSize(),
			a.worker.Control,
			workflowapplication.WorkflowContinuationWakeups(a.records.Applications().Workflows),
			a.records.Applications().Workflows.ProcessDueWorkflowContinuations,
			a.records.Applications().Workflows.ProcessWorkflowContinuation,
		)
	})
}

func startWorkflowContinuationWorkerLoop(
	ctx context.Context,
	interval time.Duration,
	batchSize int,
	control *workerplatform.Controller,
	wakeups <-chan workflowapplication.WorkflowContinuationLocator,
	process func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error),
	processExact func(context.Context, workflowapplication.WorkflowContinuationLocator, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error),
) <-chan struct{} {
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 25
	}
	return workerplatform.StartWakeableRecoveryLoop(ctx, runtimeWorkerIdentity("workflow_continuation"), interval, wakeups, func(tickCtx context.Context) (workerplatform.TickResult, error) {
		return runControlledRuntimeWorkerTick(tickCtx, control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
			result, err := process(operationCtx, batchSize, workflowapplication.WorkflowWorkerPrincipal())
			return workerplatform.TickResult{Worked: result.Processed > 0, ProcessedCount: result.Processed}, err
		})
	}, func(executeCtx context.Context, locator workflowapplication.WorkflowContinuationLocator) (workerplatform.TickResult, error) {
		return runControlledRuntimeWorkerTick(executeCtx, control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
			result, err := processExact(operationCtx, locator, workflowapplication.WorkflowWorkerPrincipal())
			return workerplatform.TickResult{Worked: result.Processed > 0, ProcessedCount: result.Processed}, err
		})
	})
}

func (a *Runtime) StartIntegrationEventWorker(ctx context.Context) {
	if a == nil || a.integrationWorkers == nil {
		return
	}
	a.startIntegrationOwnerLoop(ctx, "integration_event", "Integration event worker failed", a.integrationWorkers.ProcessDueEvents)
}

func (a *Runtime) StartPublicationHandoffWorker(ctx context.Context) {
	if a == nil || a.integrationBinding == nil || a.records == nil || a.records.Applications().PublicationHandoff == nil {
		return
	}
	a.startControlledWorker(ctx, "runtime_publication_outbox", func(workerCtx context.Context) <-chan struct{} {
		return a.records.Applications().PublicationHandoff.StartWorker(workerCtx, time.Second, 25)
	})
}

func (a *Runtime) startConnectorProviderBackgroundWorker(ctx context.Context) {
	if a == nil || a.integrationWorkers == nil {
		return
	}
	a.startIntegrationOwnerLoop(ctx, "integration_provider", "Integration Provider worker failed", a.integrationWorkers.ProcessDueProviderTasks)
}

func (a *Runtime) startIntegrationInvocationReconciliationWorker(ctx context.Context) {
	if a == nil || a.integrationWorkers == nil {
		return
	}
	a.startIntegrationOwnerLoop(ctx, "integration_reconciliation", "Integration reconciliation worker failed", a.integrationWorkers.ProcessDueReconciliations)
}

func (a *Runtime) startIntegrationCredentialExpiryWorker(ctx context.Context) {
	if a == nil || a.integrationWorkers == nil {
		return
	}
	a.startIntegrationOwnerLoop(ctx, "integration_credentials", "Integration credential worker failed", a.integrationWorkers.ProcessDueCredentialExpirations)
}

func (a *Runtime) startIntegrationOwnerLoop(ctx context.Context, name, _ string, process func(context.Context, int) (int, error)) {
	a.startControlledWorker(ctx, name, func(workerCtx context.Context) <-chan struct{} {
		interval := a.cfg.EffectiveWorkerPollInterval()
		if interval <= 0 {
			interval = time.Second
		}
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 500 {
			batch = 25
		}
		tick := func(tickCtx context.Context) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				processed, err := process(operationCtx, batch)
				// The owner returns only after its task transactions have finished.
				// Even a partially failed batch may have committed events. This is
				// only a hint: the event owner still claims committed durable rows.
				if name == "integration_provider" && processed > 0 {
					a.signalIntegrationQueue("integration_event")
				}
				return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
			})
		}
		maxInterval := max(30*time.Second, interval*30)
		wakeups := a.integrationQueueWakeup(name)
		if name == "integration_provider" {
			// One recovery slot plus one push-only slot, both inside this owner
			// supervisor/controller. A long backfill batch must not prevent a new
			// notification being admitted. Integration's existing connection
			// leases/fencing still serialize each mailbox; there are at most two
			// concurrent batches, not an unbounded goroutine per notification.
			recovery := workerplatform.StartAdaptiveLoop(workerCtx, runtimeWorkerIdentity(name), interval, maxInterval, tick)
			push := workerplatform.StartWakeableRecoveryLoop(workerCtx, runtimeWorkerIdentity(name+"_push"), maxInterval, wakeups, func(context.Context) (workerplatform.TickResult, error) {
				return workerplatform.TickResult{}, nil
			}, func(executeCtx context.Context, _ struct{}) (workerplatform.TickResult, error) {
				return tick(executeCtx)
			})
			return workerplatform.Join(recovery, push)
		}
		return workerplatform.StartWakeableAdaptiveLoop(workerCtx, runtimeWorkerIdentity(name), interval, maxInterval, wakeups, tick)
	})
}

func (a *Runtime) StartNotificationPublicationWorker(ctx context.Context) {
	if a.notificationWorkers == nil && a.notificationRelay == nil {
		return
	}
	if a.notificationRelay != nil {
		a.startControlledWorker(ctx, "notification_publication", func(workerCtx context.Context) <-chan struct{} {
			batch := a.cfg.EffectiveWorkerBatchSize()
			if batch <= 0 || batch > 100 {
				batch = 25
			}
			wakeups := a.store.WorkerWakeups().Subscribe("notification_publication", 64)
			return workerplatform.StartWakeableRecoveryLoop(workerCtx, runtimeWorkerIdentity("notification_publication"), notificationPublicationRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func(tickCtx context.Context) (workerplatform.TickResult, error) {
				return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
					processed, err := a.notificationRelay.ProcessDue(operationCtx, batch)
					return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
				})
			}, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) (workerplatform.TickResult, error) {
				return runControlledRuntimeWorkerTick(executeCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
					worked, err := a.notificationRelay.Process(operationCtx, locator)
					return workerplatform.TickResult{Worked: worked, ProcessedCount: boolCount(worked)}, err
				})
			})
		})
		return
	}
	if a.notificationWorkers != nil {
		a.startControlledWorker(ctx, "notification_publication", func(workerCtx context.Context) <-chan struct{} {
			batch := a.cfg.EffectiveWorkerBatchSize()
			if batch <= 0 {
				batch = 25
			}
			wakeups := a.store.WorkerWakeups().Subscribe("notification_publication", 64)
			publicationDone := workerplatform.StartWakeableRecoveryLoop(workerCtx, runtimeWorkerIdentity("notification_publication"), notificationPublicationRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func(tickCtx context.Context) (workerplatform.TickResult, error) {
				return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
					processed, err := a.notificationWorkers.ProcessDuePublications(operationCtx, batch)
					return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
				})
			}, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) (workerplatform.TickResult, error) {
				return runControlledRuntimeWorkerTick(executeCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
					worked, err := a.notificationWorkers.ProcessPublication(operationCtx, notificationSDKWorkLocator(locator))
					return workerplatform.TickResult{Worked: worked, ProcessedCount: boolCount(worked)}, err
				})
			})
			refreshDone := workerplatform.StartNamedLoop(workerCtx, runtimeWorkerIdentity("notification_renderer_refresh"), 5*time.Second, func(tickCtx context.Context) (workerplatform.TickResult, error) {
				return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
					return workerplatform.TickResult{}, a.notificationWorkers.RefreshPublished(operationCtx)
				})
			})
			return workerplatform.Join(publicationDone, refreshDone)
		})
	}
}

func notificationPublicationRecoveryInterval(interval time.Duration) time.Duration {
	if interval < 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func (a *Runtime) startNotificationInboxWorker(ctx context.Context) {
	if a.notificationWorkers == nil {
		return
	}
	a.startControlledWorker(ctx, "notification_inbox", func(workerCtx context.Context) <-chan struct{} {
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 100 {
			batch = 25
		}
		wakeups := a.store.WorkerWakeups().Subscribe("notification_inbox", 64)
		return workerplatform.StartWakeableRecoveryLoop(workerCtx, runtimeWorkerIdentity("notification_inbox"), notificationInboxRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func(tickCtx context.Context) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				processed, err := a.notificationWorkers.ProcessDueInboxEvents(operationCtx, batch)
				return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, err
			})
		}, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(executeCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				worked, err := a.notificationWorkers.ProcessInboxEvent(operationCtx, notificationSDKWorkLocator(locator))
				return workerplatform.TickResult{Worked: worked, ProcessedCount: boolCount(worked)}, err
			})
		})
	})
}

func notificationSDKWorkLocator(locator workerplatform.DurableTaskLocator) notificationsdk.WorkLocator {
	return notificationsdk.WorkLocator{Kind: locator.QueueKind, WorkspaceID: locator.WorkspaceID, TaskID: locator.TaskID}
}

func notificationInboxRecoveryInterval(interval time.Duration) time.Duration {
	if interval < 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func runControlledRuntimeWorkerTick(ctx context.Context, control *workerplatform.Controller, operation workerplatform.Tick) (workerplatform.TickResult, error) {
	result := workerplatform.TickResult{}
	var err error
	if control == nil || !control.RunIfAccepting(func() { result, err = operation(ctx) }) {
		return result, nil
	}
	return result, err
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (a *Runtime) startRuntimeReleaseHeartbeat(parent context.Context) {
	if a == nil || a.releaseCohort == nil || a.runtimeReleaseLease().InstanceID == "" {
		return
	}
	a.startTrackedWorker(parent, func(ctx context.Context) <-chan struct{} {
		return workerplatform.StartNamedLoop(ctx, runtimeWorkerIdentity("runtime_release_cohort"), a.releaseCohort.HeartbeatInterval(), func(tickCtx context.Context) (workerplatform.TickResult, error) {
			if a.releaseAdmission.Check() != nil {
				return workerplatform.TickResult{}, nil
			}
			lease, err := a.releaseCohort.Heartbeat(tickCtx, a.runtimeReleaseLease(), a.worker.Clock.Now())
			if err == nil {
				a.replaceRuntimeReleaseLease(lease)
				return workerplatform.TickResult{Worked: true, ProcessedCount: 1}, nil
			}
			a.releaseAdmission.Fail(err)
			if a.api != nil {
				a.api.SetDraining(true)
			}
			a.worker.Control.Drain()
			return workerplatform.TickResult{Attempt: 1}, err
		})
	})
}
