package identity

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	shaID := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name      string
		container Container
		alias     string
		want      Result
		wantError string
	}{
		{
			name: "workload label wins over valid alias and retains Compose metadata",
			container: Container{
				Name:  "/ignored",
				Image: "registry.example.com/Team/API:1.2.3",
				Labels: map[string]string{
					LabelWorkload:               " /Payments/API ",
					LabelComposeProject:         " Commerce ",
					LabelComposeService:         " Backend_API ",
					LabelComposeContainerNumber: " 2 ",
				},
			},
			alias: "alias",
			want: Result{
				WorkloadKey:            "payments/api",
				DisplayName:            "api",
				Source:                 SourceWorkloadLabel,
				ComposeProject:         "commerce",
				ComposeService:         "backend_api",
				ComposeContainerNumber: "2",
				ImageRepository:        "registry.example.com/team/api",
			},
		},
		{
			name: "complete Compose metadata wins over valid alias",
			container: Container{
				Name:   "/ignored",
				Image:  "worker:latest",
				Labels: map[string]string{LabelComposeProject: "Demo", LabelComposeService: "Worker"},
			},
			alias: "ignored-alias",
			want: Result{
				WorkloadKey:     "demo/worker",
				DisplayName:     "worker",
				Source:          SourceCompose,
				ComposeProject:  "demo",
				ComposeService:  "worker",
				ImageRepository: "worker",
			},
		},
		{
			name:      "alias wins incomplete Compose metadata",
			container: Container{Name: "/ignored", Image: "app:1", Labels: map[string]string{LabelComposeProject: "Demo"}},
			alias:     " Team/API_v2 ",
			want: Result{
				WorkloadKey:     "team/api_v2",
				DisplayName:     "api_v2",
				Source:          SourceAlias,
				ComposeProject:  "demo",
				ImageRepository: "app",
			},
		},
		{
			name:      "container name removes Docker slash",
			container: Container{Name: " /My.App-1 ", Image: "app:1"},
			want: Result{
				WorkloadKey:     "my.app-1",
				DisplayName:     "my.app-1",
				Source:          SourceContainerName,
				ImageRepository: "app",
			},
		},
		{
			name:      "image tag fallback",
			container: Container{Image: "nginx:1.27"},
			want:      Result{WorkloadKey: "nginx", DisplayName: "nginx", Source: SourceImage, ImageRepository: "nginx"},
		},
		{
			name:      "image digest fallback",
			container: Container{Image: "team/backend@sha256:" + strings.Repeat("b", 64)},
			want:      Result{WorkloadKey: "team/backend", DisplayName: "backend", Source: SourceImage, ImageRepository: "team/backend"},
		},
		{
			name:      "registry port and nested path fallback",
			container: Container{Image: "registry.example.com:5000/platform/team/api:v2"},
			want: Result{
				WorkloadKey:     "registry.example.com:5000/platform/team/api",
				DisplayName:     "api",
				Source:          SourceImage,
				ImageRepository: "registry.example.com:5000/platform/team/api",
			},
		},
		{
			name:      "bare image ID is not an identity",
			container: Container{Image: shaID},
			wantError: "workload identity is unavailable",
		},
		{
			name:      "invalid workload label does not fall through",
			container: Container{Name: "valid", Labels: map[string]string{LabelWorkload: "team//api"}},
			wantError: LabelWorkload,
		},
		{
			name:      "empty explicit workload label is invalid",
			container: Container{Name: "valid", Labels: map[string]string{LabelWorkload: ""}},
			wantError: LabelWorkload,
		},
		{
			name:      "whitespace explicit workload label is invalid",
			container: Container{Name: "valid", Labels: map[string]string{LabelWorkload: " \t "}},
			wantError: LabelWorkload,
		},
		{
			name:      "invalid alias does not fall through",
			container: Container{Name: "valid"},
			alias:     "../api",
			wantError: "invalid alias",
		},
		{
			name:      "whitespace alias is invalid",
			container: Container{Name: "valid"},
			alias:     " \t ",
			wantError: "invalid alias",
		},
		{
			name: "invalid alias errors before complete Compose selection",
			container: Container{Labels: map[string]string{
				LabelComposeProject: "demo",
				LabelComposeService: "api",
			}},
			alias:     "../api",
			wantError: "invalid alias",
		},
		{
			name: "invalid alias errors before workload label selection",
			container: Container{Labels: map[string]string{
				LabelWorkload: "demo/api",
			}},
			alias:     "../api",
			wantError: "invalid alias",
		},
		{
			name: "invalid incomplete Compose metadata falls through",
			container: Container{
				Name:   "valid-name",
				Image:  "app:1",
				Labels: map[string]string{LabelComposeService: "api service"},
			},
			want: Result{
				WorkloadKey:     "valid-name",
				DisplayName:     "valid-name",
				Source:          SourceContainerName,
				ImageRepository: "app",
			},
		},
		{
			name: "invalid complete Compose identity returns an error",
			container: Container{Labels: map[string]string{
				LabelComposeProject: "demo",
				LabelComposeService: "api service",
			}},
			wantError: LabelComposeService,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(test.container, test.alias)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Resolve() error = %v, want error containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != test.want {
				t.Errorf("Resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}
