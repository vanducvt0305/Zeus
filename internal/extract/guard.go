package extract

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// blockedIP reports whether ip is a non-public address we refuse to connect to
// by default. Tool extraction dials URLs from untrusted catalog entries, so
// this prevents SSRF to loopback, private ranges, link-local (including the
// cloud metadata endpoint 169.254.169.254), the unspecified address, and
// multicast.
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

// guardedDial resolves the target host and refuses blocked address ranges
// (unless allowPrivate). Checking at dial time — not just before — also defends
// against DNS rebinding.
func guardedDial(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ipa := range ips {
			if !allowPrivate && blockedIP(ipa.IP) {
				lastErr = fmt.Errorf("refusing to connect to non-public address %s (host %q)", ipa.IP, host)
				continue
			}
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no usable address for host %q", host)
		}
		return nil, lastErr
	}
}

// guardedTransport is an http.Transport that dials through guardedDial.
func guardedTransport(allowPrivate bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = guardedDial(allowPrivate)
	return t
}

// noCrossHostRedirect blocks redirects to a different host (which would both
// re-send any auth headers to that host and bypass the original SSRF check) and
// caps redirect depth.
func noCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("refusing cross-host redirect to %s", req.URL.Host)
	}
	return nil
}
