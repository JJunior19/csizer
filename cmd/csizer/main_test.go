package main

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantStatus   int
		wantStdout   string
		wantStderr   string
		forbidStderr string
	}{
		{
			name:       "version succeeds",
			args:       []string{"version"},
			wantStatus: 0,
			wantStdout: "csizer version=dev commit=unknown date=unknown\n",
		},
		{
			name:         "invalid command fails once on stderr",
			args:         []string{"missing"},
			wantStatus:   1,
			wantStderr:   "unknown command",
			forbidStderr: "Usage:",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			status := run(test.args, &stdout, &stderr)

			if status != test.wantStatus {
				t.Errorf("run() status = %d, want %d", status, test.wantStatus)
			}
			if test.wantStdout != "" && stdout.String() != test.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want content %q", stderr.String(), test.wantStderr)
			}
			if test.forbidStderr != "" && strings.Contains(stderr.String(), test.forbidStderr) {
				t.Errorf("stderr = %q, must not contain %q", stderr.String(), test.forbidStderr)
			}
		})
	}
}

func TestCommandContextHandlesSIGTERM(t *testing.T) {
	ctx, stop := commandContext(t.Context())
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill(SIGTERM) error = %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("command context did not cancel after SIGTERM")
	}
}
