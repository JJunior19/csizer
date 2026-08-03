package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jorgeccarhuasaroni/containersize/internal/version"
)

func TestRootCommand(t *testing.T) {
	t.Parallel()

	info := version.BuildInfo{
		Version: "1.2.3",
		Commit:  "abc123",
		Date:    "2026-08-03T00:00:00Z",
	}
	versionOutput := "csizer version=1.2.3 commit=abc123 date=2026-08-03T00:00:00Z\n"

	tests := []struct {
		name            string
		args            []string
		wantOutput      string
		wantContains    string
		wantError       string
		wantErrorOutput string
	}{
		{
			name:       "version subcommand",
			args:       []string{"version"},
			wantOutput: versionOutput,
		},
		{
			name:       "root version flag",
			args:       []string{"--version"},
			wantOutput: versionOutput,
		},
		{
			name:         "help describes recommendation limits",
			args:         []string{"--help"},
			wantContains: "Recommendations are evidence-based estimates, not load-test guarantees.",
		},
		{
			name:      "unknown command returns a silent error",
			args:      []string{"missing"},
			wantError: "unknown command",
		},
		{
			name:      "version rejects arguments without usage output",
			args:      []string{"version", "extra"},
			wantError: "unknown command",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			command := NewRoot(info, &stdout, &stderr)
			command.SetArgs(test.args)

			err := command.Execute()
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Execute() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Execute() error = %v, want error containing %q", err, test.wantError)
			}

			if got := stdout.String(); test.wantOutput != "" && got != test.wantOutput {
				t.Errorf("stdout = %q, want %q", got, test.wantOutput)
			}
			if got := stdout.String(); test.wantContains != "" && !strings.Contains(got, test.wantContains) {
				t.Errorf("stdout = %q, want content %q", got, test.wantContains)
			}
			if got := stderr.String(); got != test.wantErrorOutput {
				t.Errorf("stderr = %q, want %q", got, test.wantErrorOutput)
			}
		})
	}
}
