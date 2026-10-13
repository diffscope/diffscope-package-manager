package netpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Policy struct {
	Unrestricted bool
	AllowedHosts []string
	DisableProxy bool
}

func (p Policy) Name() string {
	if p.Unrestricted {
		return "unrestricted"
	}
	return "restricted"
}
func (p Policy) Conformance() string {
	if p.Unrestricted {
		return "client-url-override"
	}
	return "strict-publication"
}

// Returns the address validation scope for the route.
// A user-configured proxy performs destination DNS resolution.
func (p Policy) AddressValidation(raw string) string {
	if !p.DisableProxy {
		req, err := http.NewRequest("GET", raw, nil)
		if err == nil {
			if proxy, err := http.ProxyFromEnvironment(req); err == nil && proxy != nil {
				return "proxy-delegated"
			}
		}
	}
	if p.Unrestricted {
		return "unrestricted"
	}
	return "direct"
}
func Key(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Redact(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (p Policy) allowed(host string) bool {
	for _, h := range p.AllowedHosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}
func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func (p Policy) Validate(raw string) error {
	if err := repo.ValidateURL(raw, p.Unrestricted); err != nil {
		return fmt.Errorf("DOWNLOAD_POLICY_VIOLATION: %w", err)
	}
	return nil
}
func (p Policy) Client(metadata bool) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	t := &http.Transport{DisableCompression: true, MaxIdleConns: 32, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	var trustedProxies sync.Map
	if !p.DisableProxy {
		t.Proxy = func(req *http.Request) (*url.URL, error) {
			proxy, err := http.ProxyFromEnvironment(req)
			if proxy != nil {
				port := proxy.Port()
				if port == "" {
					port = "80"
					if proxy.Scheme == "https" {
						port = "443"
					}
					if proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" {
						port = "1080"
					}
				}
				trustedProxies.Store(net.JoinHostPort(proxy.Hostname(), port), true)
			}
			return proxy, err
		}
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		_, proxy := trustedProxies.Load(address)
		if p.Unrestricted || metadata || proxy || p.allowed(address) {
			return dialer.DialContext(ctx, network, address)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fmt.Errorf("DOWNLOAD_POLICY_VIOLATION: The destination address is nonpublic.")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		if last == nil {
			last = fmt.Errorf("DNS resolution returned no addresses.")
		}
		return nil, last
	}
	return &http.Client{Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("The redirect count exceeds its budget.")
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return p.Validate(req.URL.String())
	}}
}
func (p Policy) Request(ctx context.Context, client *http.Client, raw string) (*http.Response, error) {
	if err := p.Validate(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	return client.Do(req)
}
