// Package recommendation derives provider-neutral resource recommendations.
package recommendation

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jorgeccarhuasaroni/containersize/internal/analysis"
)

const (
	headroomMultiplier = 1.20
	cpuPrecision       = 0.01
	mebibyte           = int64(1024 * 1024)
	qualitySampleCount = 30
	maximumAge         = 30 * 24 * time.Hour
)

// Recommendation is a provider-neutral CPU and memory estimate.
type Recommendation struct {
	WorkloadID  int64
	CPUCores    float64
	MemoryBytes int64
	Evidence    Evidence
	Confidence  Confidence
	Assumptions []string
	Warnings    []string
}

// Evidence identifies the exact observations behind a recommendation.
type Evidence struct {
	RequestedFrom    time.Time
	RequestedTo      time.Time
	ObservedFrom     time.Time
	ObservedTo       time.Time
	Representative   analysis.RepresentativeWindow
	InputSampleIDs   []int64
	SampleCount      int
	CPU              analysis.FloatSummary
	MemoryWorkingSet analysis.ByteSummary
}

// Confidence explains the evidence quality score for a recommendation.
type Confidence struct {
	Score       float64
	Level       string
	Coverage    float64
	Recency     float64
	Variability float64
	DataQuality float64
}

// Derive produces a recommendation from one completed analysis result.
func Derive(result analysis.Result, now time.Time) (Recommendation, error) {
	if now.IsZero() {
		return Recommendation{}, errors.New("derive recommendation: current time is required")
	}
	if err := validateResult(result); err != nil {
		return Recommendation{}, fmt.Errorf("derive recommendation: %w", err)
	}
	cpu, err := withCPUHeadroom(result.Aggregate.CPU.P95)
	if err != nil {
		return Recommendation{}, fmt.Errorf("derive recommendation: CPU: %w", err)
	}
	memory, err := withMemoryHeadroom(result.Aggregate.MemoryWorking.P99)
	if err != nil {
		return Recommendation{}, fmt.Errorf("derive recommendation: memory: %w", err)
	}
	confidence := evaluateConfidence(result, now.UTC())
	return Recommendation{
		WorkloadID:  result.Aggregate.WorkloadID,
		CPUCores:    cpu,
		MemoryBytes: memory,
		Evidence: Evidence{
			RequestedFrom:    result.Request.From,
			RequestedTo:      result.Request.To,
			ObservedFrom:     result.Aggregate.ObservedFrom,
			ObservedTo:       result.Aggregate.ObservedTo,
			Representative:   result.RepresentativeWindow,
			InputSampleIDs:   append([]int64(nil), result.Aggregate.InputSampleIDs...),
			SampleCount:      len(result.Aggregate.InputSampleIDs),
			CPU:              result.Aggregate.CPU,
			MemoryWorkingSet: result.Aggregate.MemoryWorking,
		},
		Confidence: confidence,
		Assumptions: []string{
			"CPU is 120% of observed CPU P95, rounded up to 0.01 cores.",
			"Memory is 120% of observed working-set P99, rounded up to a MiB.",
			"The recommendation reflects passive observations, not a load-test capacity guarantee.",
		},
		Warnings: warningsFor(confidence),
	}, nil
}

func validateResult(result analysis.Result) error {
	if result.Request.WorkloadID <= 0 || result.Aggregate.WorkloadID != result.Request.WorkloadID {
		return errors.New("workload IDs must match and be positive")
	}
	if result.Request.From.IsZero() || result.Request.To.IsZero() || result.Request.To.Before(result.Request.From) {
		return errors.New("requested boundaries are invalid")
	}
	if result.Aggregate.ObservedFrom.IsZero() || result.Aggregate.ObservedTo.Before(result.Aggregate.ObservedFrom) {
		return errors.New("observed boundaries are invalid")
	}
	if result.Aggregate.ObservedFrom.Before(result.Request.From) || result.Aggregate.ObservedTo.After(result.Request.To) {
		return errors.New("observed boundaries are outside the requested range")
	}
	if len(result.Aggregate.InputSampleIDs) == 0 {
		return errors.New("at least one input sample is required")
	}
	if result.RepresentativeWindow.SampleCount() == 0 || result.RepresentativeWindow.StartedAt.Before(result.Aggregate.ObservedFrom) ||
		result.RepresentativeWindow.EndedAt.After(result.Aggregate.ObservedTo) {
		return errors.New("representative window is outside observed boundaries")
	}
	inputs := make(map[int64]struct{}, len(result.Aggregate.InputSampleIDs))
	for _, id := range result.Aggregate.InputSampleIDs {
		inputs[id] = struct{}{}
	}
	for _, id := range result.RepresentativeWindow.SampleIDs {
		if _, found := inputs[id]; !found {
			return errors.New("representative window samples are not aggregate inputs")
		}
	}
	return nil
}

func withCPUHeadroom(value float64) (float64, error) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("P95 must be finite and nonnegative")
	}
	return math.Ceil(value*headroomMultiplier/cpuPrecision) * cpuPrecision, nil
}

func withMemoryHeadroom(value int64) (int64, error) {
	if value < 0 || value > (math.MaxInt64-99)/120 {
		return 0, errors.New("P99 cannot be represented with headroom")
	}
	withHeadroom := (value*120 + 99) / 100
	if withHeadroom > math.MaxInt64-(mebibyte-1) {
		return 0, errors.New("P99 cannot be rounded to a MiB")
	}
	return ((withHeadroom + mebibyte - 1) / mebibyte) * mebibyte, nil
}

func evaluateConfidence(result analysis.Result, now time.Time) Confidence {
	coverage := coverage(result.Request.From, result.Request.To, result.Aggregate.ObservedFrom, result.Aggregate.ObservedTo)
	recency := recency(result.Aggregate.ObservedTo, now)
	variability := variability(result.Aggregate)
	dataQuality := dataQuality(result.Aggregate.InputSampleIDs)
	score := coverage*.35 + recency*.25 + variability*.20 + dataQuality*.20
	level := "low"
	if score >= .8 {
		level = "high"
	} else if score >= .5 {
		level = "medium"
	}
	return Confidence{Score: score, Level: level, Coverage: coverage, Recency: recency, Variability: variability, DataQuality: dataQuality}
}

func coverage(requestedFrom, requestedTo, observedFrom, observedTo time.Time) float64 {
	requested := requestedTo.Sub(requestedFrom)
	if requested <= 0 {
		return 1
	}
	return clamp(observedTo.Sub(observedFrom).Seconds() / requested.Seconds())
}

func recency(observedTo, now time.Time) float64 {
	age := now.Sub(observedTo)
	if age <= 0 {
		return 1
	}
	return clamp(1 - age.Seconds()/maximumAge.Seconds())
}

func variability(aggregate analysis.Aggregate) float64 {
	return 1 - clamp((maxRatio(aggregate.CPU.P99, aggregate.CPU.Average, float64(aggregate.MemoryWorking.P99), aggregate.MemoryWorking.Average)-1)/2)
}

func maxRatio(values ...float64) float64 {
	maximum := 1.0
	for index := 0; index < len(values); index += 2 {
		percentile, average := values[index], values[index+1]
		if average == 0 {
			if percentile > 0 {
				return 3
			}
			continue
		}
		maximum = max(maximum, percentile/average)
	}
	return maximum
}

func dataQuality(ids []int64) float64 {
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			unique[id] = struct{}{}
		}
	}
	return clamp(float64(len(unique)) / qualitySampleCount)
}

func warningsFor(confidence Confidence) []string {
	warnings := make([]string, 0, 4)
	if confidence.Coverage < .8 {
		warnings = append(warnings, "Observed coverage does not span most of the requested window.")
	}
	if confidence.Recency < .8 {
		warnings = append(warnings, "Evidence is older than six days.")
	}
	if confidence.Variability < .6 {
		warnings = append(warnings, "Observed resource usage is highly variable.")
	}
	if confidence.DataQuality < .8 {
		warnings = append(warnings, "Fewer than 24 distinct persisted samples support this recommendation.")
	}
	return warnings
}

func clamp(value float64) float64 {
	return min(1, max(0, value))
}
