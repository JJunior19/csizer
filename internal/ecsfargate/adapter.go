// Package ecsfargate maps provider-neutral recommendations to ECS Fargate task sizes.
package ecsfargate

import (
	"errors"
	"fmt"
	"math"

	"github.com/jorgeccarhuasaroni/containersize/internal/recommendation"
)

const mebibyte = int64(1024 * 1024)

// TaskSize is one valid ECS Fargate task-level CPU and memory combination.
type TaskSize struct {
	CPUUnits  int `json:"cpu_units"`
	MemoryMiB int `json:"memory_mib"`
}

// AdaptedRecommendation preserves the measured recommendation with its ECS size.
type AdaptedRecommendation struct {
	Recommendation recommendation.Recommendation `json:"recommendation"`
	TaskSize       TaskSize                      `json:"task_size"`
}

// Adapt selects the smallest valid Fargate task size that meets the recommendation.
func Adapt(input recommendation.Recommendation) (AdaptedRecommendation, error) {
	if input.CPUCores < 0 || math.IsNaN(input.CPUCores) || math.IsInf(input.CPUCores, 0) {
		return AdaptedRecommendation{}, errors.New("adapt ECS Fargate recommendation: CPU cores must be finite and nonnegative")
	}
	if input.MemoryBytes < 0 {
		return AdaptedRecommendation{}, errors.New("adapt ECS Fargate recommendation: memory bytes must be nonnegative")
	}
	requiredCPU := int(math.Ceil(input.CPUCores * 1024))
	requiredMemory := int((input.MemoryBytes + mebibyte - 1) / mebibyte)
	for _, size := range sizes {
		if size.CPUUnits >= requiredCPU && size.MemoryMiB >= requiredMemory {
			return AdaptedRecommendation{Recommendation: input, TaskSize: size}, nil
		}
	}
	return AdaptedRecommendation{}, fmt.Errorf("adapt ECS Fargate recommendation: no task size supports at least %d CPU units and %d MiB", requiredCPU, requiredMemory)
}

// Validate reports whether a task size is an exact Fargate CPU and memory pair.
func Validate(size TaskSize) error {
	for _, candidate := range sizes {
		if candidate == size {
			return nil
		}
	}
	return fmt.Errorf("invalid ECS Fargate task size: %d CPU units and %d MiB", size.CPUUnits, size.MemoryMiB)
}

var sizes = buildSizes()

func buildSizes() []TaskSize {
	result := []TaskSize{
		{CPUUnits: 256, MemoryMiB: 512}, {CPUUnits: 256, MemoryMiB: 1024}, {CPUUnits: 256, MemoryMiB: 2048},
	}
	result = append(result, rangeSizes(512, 1024, 4096, 1024)...)
	result = append(result, rangeSizes(1024, 2048, 8192, 1024)...)
	result = append(result, rangeSizes(2048, 4096, 16384, 1024)...)
	result = append(result, rangeSizes(4096, 8192, 30720, 1024)...)
	result = append(result, rangeSizes(8192, 16384, 61440, 4096)...)
	result = append(result, rangeSizes(16384, 32768, 122880, 8192)...)
	result = append(result, TaskSize{CPUUnits: 32768, MemoryMiB: 61440}, TaskSize{CPUUnits: 32768, MemoryMiB: 122880}, TaskSize{CPUUnits: 32768, MemoryMiB: 249856})
	return result
}

func rangeSizes(cpu, minimum, maximum, increment int) []TaskSize {
	result := make([]TaskSize, 0, (maximum-minimum)/increment+1)
	for memory := minimum; memory <= maximum; memory += increment {
		result = append(result, TaskSize{CPUUnits: cpu, MemoryMiB: memory})
	}
	return result
}
