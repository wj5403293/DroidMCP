// SSRF guard for the scraper. Every outbound request is gated twice: once at
// argument-parse time (validateURL on the user-provided URL) and once at dial
// time (the safe transport's DialContext re-resolves the host). The double
// check matters because a hostname that looked public when we accepted the
// request can rebind to 127.0.0.1 between then and the actual TCP dial, and
// because a 30x can redirect us to an internal address after the first hop.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	errBlockedScheme = errors.New("scheme not allowed (only http/https)")
	errBlockedHost   = errors.New("host resolves to a non-public address")
)

// allowPrivateNetworks reports whether the operator has opted in to scraping
// loopback / RFC1918 / link-local / CGNAT addresses. Off by default. Set
// DROIDMCP_SCRAPER_ALLOW_PRIVATE=1 only when you know the deployment is
// isolated and you actually need to reach an internal URL.
func allowPrivateNetworks() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DROIDMCP_SCRAPER_ALLOW_PRIVATE")))
	return v == "1" || v == "true" || v == "yes"
}

// validateURL parses raw and rejects anything that is not http/https or that
// resolves to a non-public address. Called before each fetch attempt so a
// bogus argument never even hits the network.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errBlockedScheme
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("invalid url: missing host")
	}
	return validateHost(host)
}

// validateHost is the IP-policy gate. Used both at request build time
// (validateURL) and at dial time (defense against DNS rebinding and against
// post-redirect hosts the safe client decided to follow).
func validateHost(host string) error {
	if allowPrivateNetworks() {
		return nil
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("%w: %s -> %s", errBlockedHost, host, ip.String())
		}
	}
	return nil
}

// isPublicIP returns true only for routable, non-special-use addresses. The
// list is intentionally aggressive: anything loopback, unspecified, private
// (RFC1918 / IPv6 ULA), link-local, multicast, or carrier-grade NAT is denied.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	// 100.64.0.0/10 is reserved for carrier-grade NAT and never routable on
	// the public internet, so treat it as private even though Go's IsPrivate
	// does not include it.
	if cgn := (&net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}); cgn.Contains(ip) {
		return false
	}
	return true
}

// newSafeTransport returns an http.Transport whose DialContext re-runs the
// SSRF check at the moment we actually open the socket. This catches DNS
// rebinding (resolve to public, dial to private) and, combined with
// safeCheckRedirect, also catches post-redirect dials.
func newSafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if err := validateHost(host); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// safeCheckRedirect re-validates each redirect target before the client follows
// it, so a server cannot 302 us to an internal host after we approved the
// initial public one. We also cap the chain at 5 hops.
func safeCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("stopped after 5 redirects")
	}
	return validateURL(req.URL.String())
}
