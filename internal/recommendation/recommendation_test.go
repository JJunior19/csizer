package recommendation

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/analysis"
)

func TestDerivePreservesEvidenceAndRoundsRecommendations(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	result := analysisResult(base, 30)
	recommendation, err := Derive(result, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if recommendation.CPUCores != 1 || recommendation.MemoryBytes != 120*mebibyte {
		t.Errorf("resource recommendation = %#v", recommendation)
	}
	if recommendation.Evidence.SampleCount != 30 || !reflect.DeepEqual(recommendation.Evidence.InputSampleIDs, result.Aggregate.InputSampleIDs) ||
		!recommendation.Evidence.RequestedFrom.Equal(result.Request.From) || !recommendation.Evidence.Representative.StartedAt.Equal(result.RepresentativeWindow.StartedAt) {
		t.Errorf("evidence = %#v", recommendation.Evidence)
	}
	if recommendation.Confidence.Level != "high" || len(recommendation.Assumptions) != 3 || len(recommendation.Warnings) != 0 {
		t.Errorf("recommendation explanation = %#v", recommendation)
	}
}

func TestDeriveConfidenceWarnings(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	result := analysisResult(base, 2)
	result.Aggregate.ObservedTo = base.Add(10 * time.Minute)
	result.RepresentativeWindow.EndedAt = result.Aggregate.ObservedTo
	result.Aggregate.CPU = analysis.FloatSummary{Average: .2, P95: .9, P99: 1}
	result.Aggregate.MemoryWorking = analysis.ByteSummary{Average: float64(mebibyte), P95: 4 * mebibyte, P99: 5 * mebibyte}
	recommendation, err := Derive(result, base.Add(31*24*time.Hour))
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if recommendation.Confidence.Level != "low" || recommendation.Confidence.Coverage >= .8 ||
		recommendation.Confidence.Recency != 0 || recommendation.Confidence.Variability >= .6 || recommendation.Confidence.DataQuality >= .8 {
		t.Errorf("confidence = %#v", recommendation.Confidence)
	}
	if len(recommendation.Warnings) != 4 {
		t.Errorf("warnings = %#v", recommendation.Warnings)
	}
}

func TestDeriveRejectsInvalidEvidence(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*analysis.Result)
		want   string
	}{
		{name: "mismatched workload", mutate: func(result *analysis.Result) { result.Aggregate.WorkloadID++ }, want: "workload IDs"},
		{name: "no samples", mutate: func(result *analysis.Result) { result.Aggregate.InputSampleIDs = nil }, want: "input sample"},
		{name: "observations outside request", mutate: func(result *analysis.Result) { result.Aggregate.ObservedTo = result.Request.To.Add(time.Second) }, want: "outside the requested"},
		{name: "window after observation", mutate: func(result *analysis.Result) {
			result.RepresentativeWindow.EndedAt = result.Aggregate.ObservedTo.Add(time.Second)
		}, want: "representative window"},
		{name: "window has unobserved sample", mutate: func(result *analysis.Result) { result.RepresentativeWindow.SampleIDs = []int64{99} }, want: "not aggregate inputs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := analysisResult(base, 30)
			test.mutate(&result)
			_, err := Derive(result, base.Add(time.Hour))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Derive() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func analysisResult(base time.Time, sampleCount int) analysis.Result {
	ids := make([]int64, sampleCount)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	observedTo := base.Add(time.Hour)
	return analysis.Result{
		Request: analysis.Request{WorkloadID: 7, From: base, To: observedTo},
		Aggregate: analysis.Aggregate{
			WorkloadID:     7,
			InputSampleIDs: ids,
			ObservedFrom:   base,
			ObservedTo:     observedTo,
			CPU:            analysis.FloatSummary{Average: .5, P95: .83, P99: .9, Max: 1},
			MemoryWorking:  analysis.ByteSummary{Average: float64(80 * mebibyte), P95: 90 * mebibyte, P99: 100 * mebibyte, Max: 100 * mebibyte},
		},
		RepresentativeWindow: analysis.RepresentativeWindow{SampleIDs: ids, StartedAt: base, EndedAt: observedTo},
	}
}
