package analysis

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/storage"
)

type fakeSampleSource struct {
	samples []storage.MetricSample
	request Request
	err     error
}

func (source *fakeSampleSource) MetricSamplesForWorkload(_ context.Context, workloadID int64, from, to time.Time) ([]storage.MetricSample, error) {
	source.request.WorkloadID = workloadID
	source.request.From = from
	source.request.To = to
	return source.samples, source.err
}

func TestAnalyzePreservesRequestAndUsesPersistedSamples(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	source := &fakeSampleSource{samples: []storage.MetricSample{
		{ID: 1, Timestamp: base, CPUUsageCores: 1, MemoryUsageBytes: 100, MemoryWorkingSetBytes: 80},
		{ID: 2, Timestamp: base.Add(time.Second), CPUUsageCores: 2, MemoryUsageBytes: 200, MemoryWorkingSetBytes: 160},
	}}
	result, err := Analyze(t.Context(), source, Request{WorkloadID: 12, From: base, To: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if source.request.WorkloadID != 12 || !source.request.From.Equal(base) || !source.request.To.Equal(base.Add(time.Minute)) {
		t.Errorf("sample source request = %#v", source.request)
	}
	if result.Request.MaximumGap != DefaultRepresentativeGap || !reflect.DeepEqual(result.Aggregate.InputSampleIDs, []int64{1, 2}) ||
		!reflect.DeepEqual(result.RepresentativeWindow.SampleIDs, []int64{1, 2}) {
		t.Errorf("analysis result = %#v", result)
	}
}

func TestSummarizePreservesInputsAndObservedBoundaries(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	aggregate, err := Summarize(42, []storage.MetricSample{
		{ID: 20, Timestamp: base.Add(time.Second), CPUUsageCores: 0.2, MemoryUsageBytes: 200, MemoryWorkingSetBytes: 120},
		{ID: 10, Timestamp: base, CPUUsageCores: 0.1, MemoryUsageBytes: 100, MemoryWorkingSetBytes: 80},
		{ID: 40, Timestamp: base.Add(3 * time.Second), CPUUsageCores: 0.4, MemoryUsageBytes: 400, MemoryWorkingSetBytes: 300},
		{ID: 30, Timestamp: base.Add(2 * time.Second), CPUUsageCores: 0.3, MemoryUsageBytes: 300, MemoryWorkingSetBytes: 200},
	})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if aggregate.WorkloadID != 42 || !reflect.DeepEqual(aggregate.InputSampleIDs, []int64{10, 20, 30, 40}) ||
		!aggregate.ObservedFrom.Equal(base) || !aggregate.ObservedTo.Equal(base.Add(3*time.Second)) {
		t.Fatalf("aggregate boundaries and inputs = %#v", aggregate)
	}
	if aggregate.CPU != (FloatSummary{Average: 0.25, P95: 0.4, P99: 0.4, Max: 0.4}) {
		t.Errorf("CPU summary = %#v", aggregate.CPU)
	}
	if aggregate.MemoryUsage != (ByteSummary{Average: 250, P95: 400, P99: 400, Max: 400}) ||
		aggregate.MemoryWorking != (ByteSummary{Average: 175, P95: 300, P99: 300, Max: 300}) {
		t.Errorf("memory summaries = %#v, %#v", aggregate.MemoryUsage, aggregate.MemoryWorking)
	}
}

func TestSelectRepresentativeWindow(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		samples    []storage.MetricSample
		maximumGap time.Duration
		wantIDs    []int64
		wantStart  time.Time
		wantEnd    time.Time
		wantError  string
	}{
		{
			name: "largest contiguous range",
			samples: []storage.MetricSample{
				{ID: 4, Timestamp: base.Add(32 * time.Second)},
				{ID: 1, Timestamp: base},
				{ID: 3, Timestamp: base.Add(22 * time.Second)},
				{ID: 2, Timestamp: base.Add(2 * time.Second)},
			},
			maximumGap: 5 * time.Second,
			wantIDs:    []int64{1, 2},
			wantStart:  base,
			wantEnd:    base.Add(2 * time.Second),
		},
		{
			name: "equal sized range favors latest",
			samples: []storage.MetricSample{
				{ID: 1, Timestamp: base},
				{ID: 2, Timestamp: base.Add(time.Second)},
				{ID: 3, Timestamp: base.Add(10 * time.Second)},
				{ID: 4, Timestamp: base.Add(11 * time.Second)},
			},
			maximumGap: 2 * time.Second,
			wantIDs:    []int64{3, 4},
			wantStart:  base.Add(10 * time.Second),
			wantEnd:    base.Add(11 * time.Second),
		},
		{
			name:      "positive gap required",
			samples:   []storage.MetricSample{{ID: 1, Timestamp: base}},
			wantError: "maximum gap must be positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, err := SelectRepresentativeWindow(test.samples, test.maximumGap)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("SelectRepresentativeWindow() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectRepresentativeWindow() error = %v", err)
			}
			if !reflect.DeepEqual(window.SampleIDs, test.wantIDs) || !window.StartedAt.Equal(test.wantStart) ||
				!window.EndedAt.Equal(test.wantEnd) || window.SampleCount() != len(test.wantIDs) {
				t.Errorf("window = %#v", window)
			}
		})
	}
}

func TestSummarizeRejectsIncompletePersistedSamples(t *testing.T) {
	t.Parallel()

	_, err := Summarize(1, []storage.MetricSample{{Timestamp: time.Now()}})
	if err == nil || !strings.Contains(err.Error(), "no persisted ID") {
		t.Fatalf("Summarize() error = %v, want missing persisted ID", err)
	}
}
