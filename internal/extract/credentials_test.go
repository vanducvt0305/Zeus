package extract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCredentialsResolutionOrder(t *testing.T) {
	c := NewCredentials("globaltok", map[string]map[string]string{
		"io.github.acme/search": {"Authorization": "Bearer byid"},
		"api.example.com":       {"X-API-Key": "byhost"},
		"*.corp.internal":       {"Authorization": "Bearer bywild"},
	})

	cases := []struct {
		name, mcpID, url string
		wantHeader, want string
	}{
		{"by id", "io.github.acme/search", "https://whatever/mcp", "Authorization", "Bearer byid"},
		{"by host", "other", "https://api.example.com/mcp", "X-API-Key", "byhost"},
		{"by wildcard", "other", "https://x.corp.internal/mcp", "Authorization", "Bearer bywild"},
		{"global fallback", "other", "https://random.example/mcp", "Authorization", "Bearer globaltok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := c.Headers(tc.mcpID, tc.url)
			if h[tc.wantHeader] != tc.want {
				t.Fatalf("Headers[%s] = %q, want %q (full: %v)", tc.wantHeader, h[tc.wantHeader], tc.want, h)
			}
		})
	}
}

func TestCredentialsEmptyReturnsNil(t *testing.T) {
	c := NewCredentials("", nil)
	if !c.Empty() {
		t.Fatal("expected Empty() true")
	}
	if h := c.Headers("any", "https://x/y"); h != nil {
		t.Fatalf("expected nil headers, got %v", h)
	}
}

// TestAuthHeadersReachTheWire proves the injecting client actually sends the
// resolved headers on the request — the mechanism behind authenticated
// extraction.
func TestAuthHeadersReachTheWire(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewRemoteWithAuth(5*time.Second, NewCredentials("secret", nil), true)
	client := r.httpClientFor(r.creds.Headers("mcp", srv.URL))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "Bearer secret" {
		t.Fatalf("server saw Authorization=%q, want %q", gotAuth, "Bearer secret")
	}
}

func TestNoCredentialsUsesSharedClient(t *testing.T) {
	r := NewRemote(time.Second)
	if got := r.httpClientFor(nil); got != r.httpClient {
		t.Fatal("expected shared anonymous client when no headers apply")
	}
}
