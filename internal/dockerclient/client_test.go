package dockerclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	mobyclient "github.com/moby/moby/client"
)

func TestPing(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := t.Context()
	ctx = context.WithValue(ctx, contextKey{}, "ping-context")
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Context().Value(contextKey{}) != "ping-context" {
			t.Error("Ping() did not propagate context")
		}
		if request.URL.Path != "/_ping" {
			t.Errorf("request path = %q, want /_ping", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("OK")),
		}, nil
	})}
	apiClient, err := mobyclient.New(
		mobyclient.WithHost("http://docker.test"),
		mobyclient.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("mobyclient.New() error = %v", err)
	}
	docker := &Client{apiClient: apiClient}
	if err := docker.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

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
