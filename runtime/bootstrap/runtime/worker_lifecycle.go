package runtime

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

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
		return workerplatform.StartAdaptiveLoop(workerCtx, "record_timer", interval, maxInterval, func() bool {
			return runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "record timer worker failed", func() (bool, error) {
				processed, err := service.ProcessDueForAllWorkspaces(
					workerCtx,
					time.Time{},
					batch,
					principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "process due record timers"),
				)
				return processed > 0, err
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
		return workerplatform.StartWakeableRecoveryLoop(workerCtx, "notification_channel", notificationChannelRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func() {
			runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification channel worker failed", func() (bool, error) {
				processed, err := a.notificationWorkers.ProcessDueChannelPlans(workerCtx, batch)
				return processed > 0, err
			})
		}, func(locator workerplatform.DurableTaskLocator) {
			runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification channel execution failed", func() (bool, error) {
				return a.notificationWorkers.ProcessChannelPlan(workerCtx, notificationSDKWorkLocator(locator))
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
	return workerplatform.StartWakeableRecoveryLoop(ctx, "workflow_continuation", interval, wakeups, func() {
		runLoggedRuntimeWorkerTickWork(ctx, control, "workflow continuation recovery failed", func() (bool, error) {
			result, err := process(ctx, batchSize, workflowapplication.WorkflowWorkerPrincipal())
			return result.Processed > 0, err
		})
	}, func(locator workflowapplication.WorkflowContinuationLocator) {
		runLoggedRuntimeWorkerTickWork(ctx, control, "workflow continuation execution failed", func() (bool, error) {
			result, err := processExact(ctx, locator, workflowapplication.WorkflowWorkerPrincipal())
			return result.Processed > 0, err
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

func (a *Runtime) startIntegrationOwnerLoop(ctx context.Context, name, failure string, process func(context.Context, int) (int, error)) {
	a.startControlledWorker(ctx, name, func(workerCtx context.Context) <-chan struct{} {
		interval := a.cfg.EffectiveWorkerPollInterval()
		if interval <= 0 {
			interval = time.Second
		}
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 500 {
			batch = 25
		}
		return workerplatform.StartAdaptiveLoop(workerCtx, name, interval, max(30*time.Second, interval*30), func() bool {
			return runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, failure, func() (bool, error) {
				processed, err := process(workerCtx, batch)
				return processed > 0, err
			})
		})
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
			return workerplatform.StartWakeableRecoveryLoop(workerCtx, "notification_publication", notificationPublicationRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func() {
				runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "Notification SaaS publication recovery failed", func() (bool, error) {
					processed, err := a.notificationRelay.ProcessDue(workerCtx, batch)
					return processed > 0, err
				})
			}, func(locator workerplatform.DurableTaskLocator) {
				runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "Notification SaaS publication relay failed", func() (bool, error) {
					return a.notificationRelay.Process(workerCtx, locator)
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
			publicationDone := workerplatform.StartWakeableRecoveryLoop(workerCtx, "notification_publication", notificationPublicationRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func() {
				runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification publication recovery failed", func() (bool, error) {
					processed, err := a.notificationWorkers.ProcessDuePublications(workerCtx, batch)
					return processed > 0, err
				})
			}, func(locator workerplatform.DurableTaskLocator) {
				runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification publication execution failed", func() (bool, error) {
					return a.notificationWorkers.ProcessPublication(workerCtx, notificationSDKWorkLocator(locator))
				})
			})
			refreshDone := workerplatform.StartNamedLoop(workerCtx, "notification_renderer_refresh", 5*time.Second, func() {
				runLoggedRuntimeWorkerTick(workerCtx, a.worker.Control, "notification renderer refresh failed", func() error { return a.notificationWorkers.RefreshPublished(workerCtx) })
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
		return workerplatform.StartWakeableRecoveryLoop(workerCtx, "notification_inbox", notificationInboxRecoveryInterval(a.cfg.EffectiveWorkerPollInterval()), wakeups, func() {
			runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification inbox recovery failed", func() (bool, error) {
				processed, err := a.notificationWorkers.ProcessDueInboxEvents(workerCtx, batch)
				return processed > 0, err
			})
		}, func(locator workerplatform.DurableTaskLocator) {
			runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "notification inbox execution failed", func() (bool, error) {
				return a.notificationWorkers.ProcessInboxEvent(workerCtx, notificationSDKWorkLocator(locator))
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

func runLoggedRuntimeWorkerTick(ctx context.Context, control *workerplatform.Controller, message string, operation func() error) {
	control.RunIfAccepting(func() {
		if err := operation(); err != nil && ctx.Err() == nil {
			logging.FromContext(ctx).Error(message, logging.StableErrorFields(err)...)
		}
	})
}

func runLoggedRuntimeWorkerTickWork(ctx context.Context, control *workerplatform.Controller, message string, operation func() (bool, error)) bool {
	worked := false
	control.RunIfAccepting(func() {
		var err error
		worked, err = operation()
		if err != nil && ctx.Err() == nil {
			logging.FromContext(ctx).Error(message, logging.StableErrorFields(err)...)
		}
	})
	return worked
}

func (a *Runtime) startRuntimeReleaseHeartbeat(parent context.Context) {
	if a == nil || a.releaseCohort == nil || a.runtimeReleaseLease().InstanceID == "" {
		return
	}
	a.startTrackedWorker(parent, func(ctx context.Context) <-chan struct{} {
		return workerplatform.StartNamedLoop(ctx, "runtime_release_cohort", a.releaseCohort.HeartbeatInterval(), func() {
			if a.releaseAdmission.Check() != nil {
				return
			}
			lease, err := a.releaseCohort.Heartbeat(ctx, a.runtimeReleaseLease(), a.worker.Clock.Now())
			if err == nil {
				a.replaceRuntimeReleaseLease(lease)
				return
			}
			logging.FromContext(ctx).Error("runtime release cohort heartbeat failed", logging.StableErrorFields(err)...)
			a.releaseAdmission.Fail(err)
			if a.api != nil {
				a.api.SetDraining(true)
			}
			a.worker.Control.Drain()
		})
	})
}
