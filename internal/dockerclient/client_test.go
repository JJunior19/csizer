package dockerclient

import (
	"testing"
)

func TestPrimaryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{name: "sorts aliases", names: []string{"/worker", "/api"}, want: "api"},
		{name: "removes empty names", names: []string{"", "/"}, want: ""},
		{name: "trims Docker slash", names: []string{" /backend "}, want: "backend"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := primaryName(test.names); got != test.want {
				t.Errorf("primaryName() = %q, want %q", got, test.want)
			}
		})
	}
}
