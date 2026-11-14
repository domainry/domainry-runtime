package redisdiagnostics

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/telemetry"
	redis "github.com/redis/go-redis/v9"
)

// Install observes every physical Redis boundary without recording keys,
// arguments, cached values, credentials, or server error text.
func Install(client redis.UniversalClient, component string) {
	if client != nil {
		component = boundedComponent(component)
		registerClient(component, client)
		client.AddHook(hook{component: component})
	}
}

type hook struct{ component string }

func (h hook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, address string) (connection net.Conn, returnErr error) {
		started := time.Now()
		defer func() {
			observeOperation(h.component, "dial", dependencyOutcome(returnErr), time.Since(started))
			host, _, _ := net.SplitHostPort(strings.TrimSpace(address))
			telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
				Feature: telemetry.DiagnosticFeatureRedis, Dependency: "redis", Operation: "dial",
				Input: map[string]any{"network": network, "host": host},
			}, telemetry.DependencyResult{Output: map[string]any{"connected": connection != nil}, Err: returnErr}, time.Since(started))
		}()
		return next(ctx, network, address)
	}
}

func (h hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		started := time.Now()
		err := next(ctx, command)
		outcome := dependencyOutcome(err)
		result := telemetry.DependencyResult{Err: err}
		if errors.Is(err, redis.Nil) {
			outcome = "miss"
			result = telemetry.DependencyResult{Outcome: "miss", Output: map[string]any{"found": false}}
		} else if redis.HasErrorPrefix(err, "NOSCRIPT") {
			// Script.Run deliberately probes EVALSHA first and retries with EVAL
			// when the server-side script cache is cold. That is a normal cache
			// miss, not an application or Redis availability failure.
			outcome = "miss"
			result = telemetry.DependencyResult{Outcome: "miss", Output: map[string]any{"script_cached": false}}
		}
		observeOperation(h.component, commandOperation(command.FullName()), outcome, time.Since(started))
		telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
			Feature: telemetry.DiagnosticFeatureRedis, Dependency: "redis", Operation: strings.ToLower(strings.TrimSpace(command.FullName())),
			Input: map[string]any{"argument_count": max(0, len(command.Args())-1)},
		}, result, time.Since(started))
		return err
	}
}

func (h hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		started := time.Now()
		err := next(ctx, commands)
		observeOperation(h.component, "pipeline", dependencyOutcome(err), time.Since(started))
		operations := make([]string, 0, len(commands))
		for _, command := range commands {
			operations = append(operations, strings.ToLower(strings.TrimSpace(command.FullName())))
		}
		telemetry.RecordDependencyCall(ctx, telemetry.DependencyCall{
			Feature: telemetry.DiagnosticFeatureRedis, Dependency: "redis", Operation: "pipeline",
			Input: map[string]any{"command_count": len(commands), "commands": operations},
		}, telemetry.DependencyResult{Err: err}, time.Since(started))
		return err
	}
}

var _ redis.Hook = hook{}
