package extract

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,  // loopback
		"10.1.2.3":        true,  // private
		"192.168.0.1":     true,  // private
		"169.254.169.254": true,  // link-local (cloud metadata)
		"0.0.0.0":         true,  // unspecified
		"8.8.8.8":         false, // public
		"1.1.1.1":         false, // public
	}
	for ip, want := range cases {
		if got := blockedIP(net.ParseIP(ip)); got != want {
			t.Errorf("blockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestGuardedDialBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	// Default (SSRF guard on): connecting to the loopback test server is refused.
	_, err := guardedDial(false)(context.Background(), "tcp", addr)
	if err == nil {
		t.Fatal("expected guarded dial to refuse loopback address")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("unexpected error: %v", err)
	}

	// allowPrivate disables the guard.
	conn, err := guardedDial(true)(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("allowPrivate dial should succeed: %v", err)
	}
	conn.Close()
}
