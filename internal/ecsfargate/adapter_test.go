package ecsfargate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jorgeccarhuasaroni/containersize/internal/recommendation"
)

func TestAdaptSelectsSmallestCompatibleTaskSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   recommendation.Recommendation
		want    TaskSize
		wantErr string
	}{
		{name: "small workload", input: recommendation.Recommendation{CPUCores: .1, MemoryBytes: 500 * mebibyte}, want: TaskSize{CPUUnits: 256, MemoryMiB: 512}},
		{name: "memory requires larger CPU tier", input: recommendation.Recommendation{CPUCores: .2, MemoryBytes: 3 * 1024 * mebibyte}, want: TaskSize{CPUUnits: 512, MemoryMiB: 3072}},
		{name: "CPU rounds to next tier", input: recommendation.Recommendation{CPUCores: .6, MemoryBytes: 2 * 1024 * mebibyte}, want: TaskSize{CPUUnits: 1024, MemoryMiB: 2048}},
		{name: "unsupported capacity", input: recommendation.Recommendation{CPUCores: 33, MemoryBytes: mebibyte}, wantErr: "no task size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapted, err := Adapt(test.input)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Adapt() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Adapt() error = %v", err)
			}
			if adapted.TaskSize != test.want || !reflect.DeepEqual(adapted.Recommendation, test.input) {
				t.Errorf("Adapt() = %#v, want task size %#v and original recommendation", adapted, test.want)
			}
		})
	}
}

func TestValidateRecognizesFargateCombinations(t *testing.T) {
	t.Parallel()

	if err := Validate(TaskSize{CPUUnits: 8192, MemoryMiB: 20480}); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
	if err := Validate(TaskSize{CPUUnits: 256, MemoryMiB: 1536}); err == nil || !strings.Contains(err.Error(), "invalid ECS Fargate") {
		t.Fatalf("Validate(invalid) error = %v", err)
	}
}
