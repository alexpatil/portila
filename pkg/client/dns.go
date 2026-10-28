package client

import (
	"context"
	"net"
	"runtime"
	"time"
)

// newDNSResolver returns a net.Resolver. On Linux, Windows, and macOS, it defaults
// to net.DefaultResolver to respect native OS settings (VPN, local domains, /etc/hosts).
// If explicit upstream servers are passed or when running on Android (which lacks /etc/resolv.conf),
// it configures custom upstream servers with PreferGo.
func newDNSResolver(servers []string) *net.Resolver {
	if len(servers) == 0 {
		if runtime.GOOS == "android" {
			servers = []string{"1.1.1.1:53", "8.8.8.8:53"}
		} else {
			return net.DefaultResolver
		}
	}

	normalized := make([]string, 0, len(servers))
	for _, s := range servers {
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53")
		}
		normalized = append(normalized, s)
	}

	d := &net.Dialer{Timeout: 5 * time.Second}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, srv := range normalized {
				conn, err := d.DialContext(ctx, network, srv)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}
