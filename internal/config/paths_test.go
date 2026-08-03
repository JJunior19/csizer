package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		goos          string
		environment   map[string]string
		home          string
		homeError     error
		want          Paths
		wantError     string
		wantHomeCalls int
	}{
		{
			name: "macOS defaults",
			goos: "darwin",
			home: "/Users/alex",
			want: Paths{
				Database: "/Users/alex/Library/Application Support/ContainerSize/containersize.db",
				Config:   "/Users/alex/Library/Application Support/ContainerSize/config.yaml",
			},
			wantHomeCalls: 1,
		},
		{
			name: "macOS ignores XDG locations",
			goos: "darwin",
			environment: map[string]string{
				dataHomeEnv:   "/xdg/data",
				configHomeEnv: "/xdg/config",
			},
			home: "/Users/alex",
			want: Paths{
				Database: "/Users/alex/Library/Application Support/ContainerSize/containersize.db",
				Config:   "/Users/alex/Library/Application Support/ContainerSize/config.yaml",
			},
			wantHomeCalls: 1,
		},
		{
			name: "Linux defaults",
			goos: "linux",
			home: "/home/alex",
			want: Paths{
				Database: "/home/alex/.local/share/containersize/containersize.db",
				Config:   "/home/alex/.config/containersize/config.yaml",
			},
			wantHomeCalls: 1,
		},
		{
			name: "Linux XDG locations",
			goos: "linux",
			environment: map[string]string{
				dataHomeEnv:   "/xdg/data",
				configHomeEnv: "/xdg/config",
			},
			homeError: errors.New("home should not be read"),
			want: Paths{
				Database: "/xdg/data/containersize/containersize.db",
				Config:   "/xdg/config/containersize/config.yaml",
			},
		},
		{
			name: "explicit overrides win",
			goos: "linux",
			environment: map[string]string{
				databasePathEnv: "/custom/data.db",
				configPathEnv:   "/custom/settings.yaml",
				dataHomeEnv:     "/xdg/data",
				configHomeEnv:   "/xdg/config",
			},
			homeError: errors.New("home should not be read"),
			want: Paths{
				Database: "/custom/data.db",
				Config:   "/custom/settings.yaml",
			},
		},
		{
			name: "partial override avoids unnecessary home lookup",
			goos: "linux",
			environment: map[string]string{
				databasePathEnv: "/custom/data.db",
				configHomeEnv:   "/xdg/config",
			},
			homeError: errors.New("home should not be read"),
			want: Paths{
				Database: "/custom/data.db",
				Config:   "/xdg/config/containersize/config.yaml",
			},
		},
		{
			name: "empty XDG values use Linux defaults",
			goos: "linux",
			environment: map[string]string{
				dataHomeEnv:   "",
				configHomeEnv: "",
			},
			home: "/home/alex",
			want: Paths{
				Database: "/home/alex/.local/share/containersize/containersize.db",
				Config:   "/home/alex/.config/containersize/config.yaml",
			},
			wantHomeCalls: 1,
		},
		{
			name: "empty database override fails",
			goos: "linux",
			environment: map[string]string{
				databasePathEnv: "",
			},
			wantError: "CONTAINERSIZE_DB_PATH is set but empty",
		},
		{
			name: "empty config override fails",
			goos: "darwin",
			environment: map[string]string{
				configPathEnv: "",
			},
			wantError: "CONTAINERSIZE_CONFIG_PATH is set but empty",
		},
		{
			name:          "home lookup error is clear",
			goos:          "linux",
			homeError:     errors.New("not found"),
			wantError:     "home directory: not found",
			wantHomeCalls: 1,
		},
		{
			name:          "empty home fails",
			goos:          "darwin",
			wantError:     "home directory is empty",
			wantHomeCalls: 1,
		},
		{
			name:      "unsupported operating system fails",
			goos:      "windows",
			wantError: `unsupported operating system "windows"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			lookupEnv := func(name string) (string, bool) {
				value, ok := test.environment[name]
				return value, ok
			}
			homeCalls := 0
			userHomeDir := func() (string, error) {
				homeCalls++
				return test.home, test.homeError
			}

			got, err := resolvePaths(test.goos, lookupEnv, userHomeDir)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("resolvePaths() error = %v", err)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Errorf("resolvePaths() = %#v, want %#v", got, test.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("resolvePaths() error = %v, want error containing %q", err, test.wantError)
			}

			if homeCalls != test.wantHomeCalls {
				t.Errorf("home lookup calls = %d, want %d", homeCalls, test.wantHomeCalls)
			}
		})
	}
}
