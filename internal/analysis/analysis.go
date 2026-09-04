// Package analysis derives reproducible workload statistics from stored samples.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

// DefaultRepresentativeGap treats samples more than two idle intervals apart
// as separate observation ranges until a caller provides a workload policy.
const DefaultRepresentativeGap = 20 * time.Second

// SampleSource provides the persisted sample boundary needed for analysis.
type SampleSource interface {
	MetricSamplesForWorkload(context.Context, int64, time.Time, time.Time) ([]storage.MetricSample, error)
}

// Request fixes the persisted sample range and continuity rule for one analysis.
type Request struct {
	WorkloadID int64         `json:"workload_id"`
	From       time.Time     `json:"from"`
	To         time.Time     `json:"to"`
	MaximumGap time.Duration `json:"maximum_gap"`
}

// Result preserves the requested and observed boundaries with the derived data.
type Result struct {
	Request              Request              `json:"request"`
	Aggregate            Aggregate            `json:"aggregate"`
	RepresentativeWindow RepresentativeWindow `json:"representative_window"`
}

// FloatSummary describes a CPU distribution observed in one analysis window.
type FloatSummary struct {
	Average float64 `json:"average"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
}

// ByteSummary describes a memory distribution observed in one analysis window.
type ByteSummary struct {
	Average float64 `json:"average"`
	P95     int64   `json:"p95"`
	P99     int64   `json:"p99"`
	Max     int64   `json:"max"`
}

// Aggregate retains the exact input set and time boundaries for a workload summary.
type Aggregate struct {
	WorkloadID     int64        `json:"workload_id"`
	InputSampleIDs []int64      `json:"input_sample_ids"`
	ObservedFrom   time.Time    `json:"observed_from"`
	ObservedTo     time.Time    `json:"observed_to"`
	CPU            FloatSummary `json:"cpu"`
	MemoryUsage    ByteSummary  `json:"memory_usage"`
	MemoryWorking  ByteSummary  `json:"memory_working_set"`
}

// RepresentativeWindow is the most densely observed contiguous sample range.
type RepresentativeWindow struct {
	SampleIDs []int64   `json:"sample_ids"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// SampleCount returns the number of persisted samples in the selected window.
func (window RepresentativeWindow) SampleCount() int {
	return len(window.SampleIDs)
}

// Analyze loads an explicit persisted range and derives its aggregate and window.
func Analyze(ctx context.Context, source SampleSource, request Request) (Result, error) {
	if source == nil {
		return Result{}, errors.New("analyze workload: sample source is nil")
	}
	if request.WorkloadID <= 0 {
		return Result{}, errors.New("analyze workload: workload ID is required")
	}
	if request.From.IsZero() || request.To.IsZero() {
		return Result{}, errors.New("analyze workload: boundaries are required")
	}
	if request.To.Before(request.From) {
		return Result{}, errors.New("analyze workload: to cannot precede from")
	}
	if request.MaximumGap == 0 {
		request.MaximumGap = DefaultRepresentativeGap
	}
	if request.MaximumGap < 0 {
		return Result{}, errors.New("analyze workload: maximum gap must be positive")
	}

	samples, err := source.MetricSamplesForWorkload(ctx, request.WorkloadID, request.From, request.To)
	if err != nil {
		return Result{}, fmt.Errorf("analyze workload: load samples: %w", err)
	}
	aggregate, err := Summarize(request.WorkloadID, samples)
	if err != nil {
		return Result{}, err
	}
	window, err := SelectRepresentativeWindow(samples, request.MaximumGap)
	if err != nil {
		return Result{}, err
	}
	return Result{Request: request, Aggregate: aggregate, RepresentativeWindow: window}, nil
}

// Summarize calculates workload statistics without discarding the samples used.
func Summarize(workloadID int64, samples []storage.MetricSample) (Aggregate, error) {
	if workloadID <= 0 {
		return Aggregate{}, errors.New("summarize workload: workload ID is required")
	}
	ordered, err := orderedSamples(samples)
	if err != nil {
		return Aggregate{}, fmt.Errorf("summarize workload: %w", err)
	}

	inputIDs := make([]int64, len(ordered))
	cpu := make([]float64, len(ordered))
	memoryUsage := make([]int64, len(ordered))
	memoryWorking := make([]int64, len(ordered))
	for index, sample := range ordered {
		inputIDs[index] = sample.ID
		cpu[index] = sample.CPUUsageCores
		memoryUsage[index] = sample.MemoryUsageBytes
		memoryWorking[index] = sample.MemoryWorkingSetBytes
	}

	return Aggregate{
		WorkloadID:     workloadID,
		InputSampleIDs: inputIDs,
		ObservedFrom:   ordered[0].Timestamp,
		ObservedTo:     ordered[len(ordered)-1].Timestamp,
		CPU:            summarizeFloats(cpu),
		MemoryUsage:    summarizeBytes(memoryUsage),
		MemoryWorking:  summarizeBytes(memoryWorking),
	}, nil
}

// SelectRepresentativeWindow selects the largest contiguous observed range.
// Equal-sized ranges favor longer duration, then the more recent range.
func SelectRepresentativeWindow(samples []storage.MetricSample, maximumGap time.Duration) (RepresentativeWindow, error) {
	if maximumGap <= 0 {
		return RepresentativeWindow{}, errors.New("select representative window: maximum gap must be positive")
	}
	ordered, err := orderedSamples(samples)
	if err != nil {
		return RepresentativeWindow{}, fmt.Errorf("select representative window: %w", err)
	}

	bestStart, bestEnd := 0, 1
	currentStart := 0
	for index := 1; index <= len(ordered); index++ {
		if index != len(ordered) && ordered[index].Timestamp.Sub(ordered[index-1].Timestamp) <= maximumGap {
			continue
		}
		if betterWindow(ordered, currentStart, index, bestStart, bestEnd) {
			bestStart, bestEnd = currentStart, index
		}
		currentStart = index
	}

	ids := make([]int64, bestEnd-bestStart)
	for index, sample := range ordered[bestStart:bestEnd] {
		ids[index] = sample.ID
	}
	return RepresentativeWindow{
		SampleIDs: ids,
		StartedAt: ordered[bestStart].Timestamp,
		EndedAt:   ordered[bestEnd-1].Timestamp,
	}, nil
}

func orderedSamples(samples []storage.MetricSample) ([]storage.MetricSample, error) {
	if len(samples) == 0 {
		return nil, errors.New("at least one metric sample is required")
	}
	ordered := append([]storage.MetricSample(nil), samples...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Timestamp.Equal(ordered[right].Timestamp) {
			return ordered[left].ID < ordered[right].ID
		}
		return ordered[left].Timestamp.Before(ordered[right].Timestamp)
	})
	for index, sample := range ordered {
		if sample.ID <= 0 {
			return nil, fmt.Errorf("sample %d has no persisted ID", index)
		}
		if sample.Timestamp.IsZero() {
			return nil, fmt.Errorf("sample %d has no timestamp", index)
		}
		if sample.CPUUsageCores < 0 || math.IsNaN(sample.CPUUsageCores) || math.IsInf(sample.CPUUsageCores, 0) {
			return nil, fmt.Errorf("sample %d has invalid CPU usage", index)
		}
		if sample.MemoryUsageBytes < 0 || sample.MemoryWorkingSetBytes < 0 {
			return nil, fmt.Errorf("sample %d has invalid memory usage", index)
		}
	}
	return ordered, nil
}

func betterWindow(samples []storage.MetricSample, candidateStart, candidateEnd, bestStart, bestEnd int) bool {
	candidateCount := candidateEnd - candidateStart
	bestCount := bestEnd - bestStart
	if candidateCount != bestCount {
		return candidateCount > bestCount
	}
	candidateDuration := samples[candidateEnd-1].Timestamp.Sub(samples[candidateStart].Timestamp)
	bestDuration := samples[bestEnd-1].Timestamp.Sub(samples[bestStart].Timestamp)
	if candidateDuration != bestDuration {
		return candidateDuration > bestDuration
	}
	return samples[candidateStart].Timestamp.After(samples[bestStart].Timestamp)
}

func summarizeFloats(values []float64) FloatSummary {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var total float64
	for _, value := range sorted {
		total += value
	}
	return FloatSummary{
		Average: total / float64(len(sorted)),
		P95:     floatPercentile(sorted, 0.95),
		P99:     floatPercentile(sorted, 0.99),
		Max:     sorted[len(sorted)-1],
	}
}

func summarizeBytes(values []int64) ByteSummary {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	var total float64
	for _, value := range sorted {
		total += float64(value)
	}
	return ByteSummary{
		Average: total / float64(len(sorted)),
		P95:     intPercentile(sorted, 0.95),
		P99:     intPercentile(sorted, 0.99),
		Max:     sorted[len(sorted)-1],
	}
}

func floatPercentile(sorted []float64, percentile float64) float64 {
	return sorted[percentileIndex(len(sorted), percentile)]
}

func intPercentile(sorted []int64, percentile float64) int64 {
	return sorted[percentileIndex(len(sorted), percentile)]
}

// percentileIndex uses nearest-rank semantics so percentiles are observed values.
func percentileIndex(length int, percentile float64) int {
	return int(math.Ceil(float64(length)*percentile)) - 1
}
