package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jorgeccarhuasaroni/containersize/internal/analysis"
	"github.com/jorgeccarhuasaroni/containersize/internal/ecsfargate"
	"github.com/jorgeccarhuasaroni/containersize/internal/identity"
	"github.com/jorgeccarhuasaroni/containersize/internal/recommendation"
	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

const outputVersion = "v1"

func newWorkloadCommands(resolvePaths PathResolver, open WorkloadStorageOpener, newDocker DockerClientFactory) []*cobra.Command {
	trackJSON := false
	track := &cobra.Command{
		Use:   "track WORKLOAD",
		Short: "Track a discovered Docker workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runTrack(command, resolvePaths, open, newDocker, args[0], trackJSON)
		},
	}
	track.Flags().BoolVar(&trackJSON, "json", false, "Emit machine-readable JSON")

	listJSON := false
	list := &cobra.Command{
		Use:   "list",
		Short: "List tracked workloads",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runList(command, resolvePaths, open, listJSON)
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "Emit machine-readable JSON")

	inspectJSON := false
	inspect := &cobra.Command{
		Use:   "inspect WORKLOAD",
		Short: "Inspect stored workload observations",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runInspect(command, resolvePaths, open, args[0], inspectJSON)
		},
	}
	inspect.Flags().BoolVar(&inspectJSON, "json", false, "Emit machine-readable JSON")

	recommendJSON := false
	provider := "ecs-fargate"
	recommend := &cobra.Command{
		Use:   "recommend WORKLOAD",
		Short: "Recommend ECS Fargate task resources for a workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return runRecommend(command, resolvePaths, open, args[0], provider, recommendJSON)
		},
	}
	recommend.Flags().StringVar(&provider, "provider", provider, "Target provider")
	recommend.Flags().BoolVar(&recommendJSON, "json", false, "Emit machine-readable JSON")

	return []*cobra.Command{track, list, inspect, recommend}
}

func runTrack(command *cobra.Command, resolvePaths PathResolver, open WorkloadStorageOpener, newDocker DockerClientFactory, selector string, asJSON bool) error {
	return withWorkloadStore(command, resolvePaths, open, func(store WorkloadStore) (err error) {
		if newDocker == nil {
			return errors.New("create Docker client: factory is nil")
		}
		docker, err := newDocker()
		if err != nil {
			return fmt.Errorf("create Docker client: %w", err)
		}
		if docker == nil {
			return errors.New("create Docker client: factory returned nil")
		}
		defer func() {
			if closeErr := docker.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close Docker client: %w", closeErr))
			}
		}()
		resolved, err := resolveDockerWorkload(command.Context(), docker, selector)
		if err != nil {
			return err
		}
		workload, err := store.EnsureWorkload(command.Context(), storage.Workload{
			WorkloadKey:     resolved.WorkloadKey,
			DisplayName:     resolved.DisplayName,
			ComposeProject:  resolved.ComposeProject,
			ComposeService:  resolved.ComposeService,
			ImageRepository: resolved.ImageRepository,
		})
		if err != nil {
			return fmt.Errorf("track workload %q: %w", selector, err)
		}
		if !workload.TrackingEnabled {
			workload, err = store.SetTrackingEnabled(command.Context(), workload.ID, true)
			if err != nil {
				return fmt.Errorf("enable workload %q: %w", workload.WorkloadKey, err)
			}
		}
		if asJSON {
			return writeJSON(command, struct {
				Version  string           `json:"version"`
				Workload storage.Workload `json:"workload"`
			}{Version: outputVersion, Workload: workload})
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "Tracking: %s (%s)\n", workload.DisplayName, workload.WorkloadKey)
		return err
	})
}

func runList(command *cobra.Command, resolvePaths PathResolver, open WorkloadStorageOpener, asJSON bool) error {
	return withWorkloadStore(command, resolvePaths, open, func(store WorkloadStore) error {
		workloads, err := store.ListWorkloads(command.Context())
		if err != nil {
			return fmt.Errorf("list workloads: %w", err)
		}
		if asJSON {
			return writeJSON(command, struct {
				Version   string             `json:"version"`
				Workloads []storage.Workload `json:"workloads"`
			}{Version: outputVersion, Workloads: workloads})
		}
		writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "WORKLOAD\tKEY\tTRACKING"); err != nil {
			return fmt.Errorf("write workload list: %w", err)
		}
		for _, workload := range workloads {
			state := "disabled"
			if workload.TrackingEnabled {
				state = "enabled"
			}
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", workload.DisplayName, workload.WorkloadKey, state); err != nil {
				return fmt.Errorf("write workload list: %w", err)
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("write workload list: %w", err)
		}
		return nil
	})
}

func runInspect(command *cobra.Command, resolvePaths PathResolver, open WorkloadStorageOpener, selector string, asJSON bool) error {
	return withWorkloadStore(command, resolvePaths, open, func(store WorkloadStore) error {
		workload, result, err := analyzeStoredWorkload(command.Context(), store, selector)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(command, struct {
				Version  string           `json:"version"`
				Workload storage.Workload `json:"workload"`
				Analysis analysis.Result  `json:"analysis"`
			}{Version: outputVersion, Workload: workload, Analysis: result})
		}
		return writeInspection(command, workload, result)
	})
}

func runRecommend(command *cobra.Command, resolvePaths PathResolver, open WorkloadStorageOpener, selector, provider string, asJSON bool) error {
	if provider != "ecs-fargate" {
		return fmt.Errorf("unsupported provider %q: only ecs-fargate is available", provider)
	}
	return withWorkloadStore(command, resolvePaths, open, func(store WorkloadStore) error {
		workload, result, err := analyzeStoredWorkload(command.Context(), store, selector)
		if err != nil {
			return err
		}
		derived, err := recommendation.Derive(result, time.Now())
		if err != nil {
			return fmt.Errorf("derive recommendation for %q: %w", workload.WorkloadKey, err)
		}
		adapted, err := ecsfargate.Adapt(derived)
		if err != nil {
			return fmt.Errorf("adapt recommendation for ECS Fargate: %w", err)
		}
		if asJSON {
			return writeJSON(command, struct {
				Version        string                           `json:"version"`
				Workload       storage.Workload                 `json:"workload"`
				Recommendation recommendation.Recommendation    `json:"recommendation"`
				ECSFargate     ecsfargate.AdaptedRecommendation `json:"ecs_fargate"`
			}{Version: outputVersion, Workload: workload, Recommendation: derived, ECSFargate: adapted})
		}
		return writeRecommendation(command, workload, adapted)
	})
}

func withWorkloadStore(command *cobra.Command, resolvePaths PathResolver, open WorkloadStorageOpener, run func(WorkloadStore) error) (err error) {
	if resolvePaths == nil {
		return errors.New("resolve paths: resolver is nil")
	}
	paths, err := resolvePaths()
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	if open == nil {
		return errors.New("open workload storage: opener is nil")
	}
	store, err := open(command.Context(), paths.Database)
	if err != nil {
		return fmt.Errorf("open workload storage: %w", err)
	}
	if store == nil {
		return errors.New("open workload storage: opener returned nil")
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close workload storage: %w", closeErr))
		}
	}()
	return run(store)
}

func resolveDockerWorkload(ctx context.Context, docker DockerClient, selector string) (identity.Result, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "" {
		return identity.Result{}, errors.New("track workload: selector is required")
	}
	containers, err := docker.List(ctx)
	if err != nil {
		return identity.Result{}, fmt.Errorf("list Docker containers: %w", err)
	}
	matches := make(map[string]identity.Result)
	for _, container := range containers {
		resolved, err := identity.Resolve(identity.Container{Name: container.Name, Image: container.Image, Labels: container.Labels}, "")
		if err != nil {
			return identity.Result{}, fmt.Errorf("resolve container %q: %w", displayContainerName(container), err)
		}
		if matchesSelector(selector, resolved.WorkloadKey, resolved.DisplayName, resolved.ComposeService, container.Name) {
			matches[resolved.WorkloadKey] = resolved
		}
	}
	if len(matches) == 0 {
		return identity.Result{}, fmt.Errorf("track workload %q: no matching Docker workload", selector)
	}
	if len(matches) > 1 {
		keys := make([]string, 0, len(matches))
		for key := range matches {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return identity.Result{}, fmt.Errorf("track workload %q: selector is ambiguous (%s)", selector, strings.Join(keys, ", "))
	}
	for _, resolved := range matches {
		return resolved, nil
	}
	panic("unreachable")
}

func analyzeStoredWorkload(ctx context.Context, store WorkloadStore, selector string) (storage.Workload, analysis.Result, error) {
	workload, err := selectStoredWorkload(ctx, store, selector)
	if err != nil {
		return storage.Workload{}, analysis.Result{}, err
	}
	from, to, err := store.MetricSampleBoundsForWorkload(ctx, workload.ID)
	if err != nil {
		return storage.Workload{}, analysis.Result{}, fmt.Errorf("inspect workload %q: %w", workload.WorkloadKey, err)
	}
	result, err := analysis.Analyze(ctx, store, analysis.Request{WorkloadID: workload.ID, From: from, To: to})
	if err != nil {
		return storage.Workload{}, analysis.Result{}, fmt.Errorf("analyze workload %q: %w", workload.WorkloadKey, err)
	}
	return workload, result, nil
}

func selectStoredWorkload(ctx context.Context, store WorkloadStore, selector string) (storage.Workload, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "" {
		return storage.Workload{}, errors.New("workload selector is required")
	}
	workloads, err := store.ListWorkloads(ctx)
	if err != nil {
		return storage.Workload{}, fmt.Errorf("list workloads: %w", err)
	}
	matches := make([]storage.Workload, 0, 1)
	for _, workload := range workloads {
		if matchesSelector(selector, workload.WorkloadKey, workload.DisplayName, workload.ComposeService) {
			matches = append(matches, workload)
		}
	}
	if len(matches) == 0 {
		return storage.Workload{}, fmt.Errorf("workload %q is not tracked", selector)
	}
	if len(matches) > 1 {
		keys := make([]string, len(matches))
		for index, workload := range matches {
			keys[index] = workload.WorkloadKey
		}
		sort.Strings(keys)
		return storage.Workload{}, fmt.Errorf("workload %q is ambiguous (%s)", selector, strings.Join(keys, ", "))
	}
	return matches[0], nil
}

func matchesSelector(selector string, values ...string) bool {
	for _, value := range values {
		if strings.EqualFold(selector, strings.TrimSpace(value)) || strings.EqualFold(selector, strings.TrimPrefix(strings.TrimSpace(value), "/")) {
			return true
		}
	}
	return false
}

func writeInspection(command *cobra.Command, workload storage.Workload, result analysis.Result) error {
	_, err := fmt.Fprintf(command.OutOrStdout(), "Workload: %s (%s)\nSamples: %d\nObserved: %s to %s\nCPU cores: avg=%.2f p95=%.2f p99=%.2f\nMemory working set: avg=%.0f p95=%d p99=%d bytes\n", workload.DisplayName, workload.WorkloadKey, len(result.Aggregate.InputSampleIDs), result.Aggregate.ObservedFrom.Format(time.RFC3339), result.Aggregate.ObservedTo.Format(time.RFC3339), result.Aggregate.CPU.Average, result.Aggregate.CPU.P95, result.Aggregate.CPU.P99, result.Aggregate.MemoryWorking.Average, result.Aggregate.MemoryWorking.P95, result.Aggregate.MemoryWorking.P99)
	return err
}

func writeRecommendation(command *cobra.Command, workload storage.Workload, adapted ecsfargate.AdaptedRecommendation) error {
	derived := adapted.Recommendation
	if _, err := fmt.Fprintf(command.OutOrStdout(), "Workload: %s (%s)\nRecommended CPU: %.2f cores\nRecommended memory: %d bytes\nECS Fargate: cpu=%d memory=%d MiB\nConfidence: %s (%.2f)\n", workload.DisplayName, workload.WorkloadKey, derived.CPUCores, derived.MemoryBytes, adapted.TaskSize.CPUUnits, adapted.TaskSize.MemoryMiB, derived.Confidence.Level, derived.Confidence.Score); err != nil {
		return err
	}
	for _, warning := range derived.Warnings {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(command *cobra.Command, value any) error {
	if err := json.NewEncoder(command.OutOrStdout()).Encode(value); err != nil {
		return fmt.Errorf("write JSON output: %w", err)
	}
	return nil
}

var _ analysis.SampleSource = WorkloadStore(nil)
