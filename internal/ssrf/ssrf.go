// Package ssrf provides consolidated SSRF protection utilities.
//
// This replaces the previously duplicated private-IP checking logic in
// internal/httpfetch, internal/tools, and federation (v9.0 L-9).
package ssrf

import (
	"context"
	"fmt"
	"net"
)

// PrivateRanges is the canonical set of CIDR ranges considered private or
// reserved.  They are parsed once at package init time.
var PrivateRanges = []*net.IPNet{
	mustParseCIDR("0.0.0.0/8"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("100.64.0.0/10"),
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("::1/128"),
	mustParseCIDR("fc00::/7"),
	mustParseCIDR("fe80::/10"),
}

// IsPrivateIP returns true if the IP is in a private or reserved range.
func IsPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, r := range PrivateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// ResolveHost resolves a hostname to IPs. If the host is already an IP
// literal, it is returned directly.
func ResolveHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("SSRF: DNS resolution failed for %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("SSRF: DNS resolution returned no records for %q", host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// RejectPrivateIPs resolves the host and returns an error if any resolved IP
// falls within a private/reserved range.  If allowLoopback is true, loopback
// addresses (127.x / ::1) are permitted.
func RejectPrivateIPs(ctx context.Context, host string, allowLoopback bool) error {
	ips, err := ResolveHost(ctx, host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if allowLoopback && ip.IsLoopback() {
			continue
		}
		if IsPrivateIP(ip) {
			return fmt.Errorf("SSRF protection — host %q resolves to private IP %s", host, ip)
		}
	}
	return nil
}

func mustParseCIDR(s string) *net.IPNet {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("ssrf: invalid CIDR %q: %v", s, err))
	}
	return network
}
