// Package netguard implements the SSRF address policy for relay targets.
//
// Relay targets are registered by remote parties, so their addresses are
// untrusted and must be policy-checked before HookRun dials them (D-08).
// Two tiers are supported:
//
//   - Default: block link-local ranges only (169.254.0.0/16 — which contains
//     the cloud metadata endpoint 169.254.169.254 — and fe80::/10). Loopback
//     and RFC1918 stay allowed so local relay clusters keep working.
//   - Strict (relay_block_private=true): additionally block loopback,
//     RFC1918 and IPv6 ULA (fc00::/7).
package netguard

import (
	"fmt"
	"net"
)

var (
	_, rfc1918_10, _  = net.ParseCIDR("10.0.0.0/8")
	_, rfc1918_172, _ = net.ParseCIDR("172.16.0.0/12")
	_, rfc1918_192, _ = net.ParseCIDR("192.168.0.0/16")
	_, ula, _         = net.ParseCIDR("fc00::/7")
)

// CheckIP reports whether ip is allowed under the given policy tier.
// A nil IP is always rejected (fail closed). IPv4-mapped IPv6 addresses
// (::ffff:a.b.c.d) are normalized to IPv4 before classification.
func CheckIP(ip net.IP, blockPrivate bool) error {
	if ip == nil {
		return fmt.Errorf("netguard: nil IP address rejected")
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// Always blocked: link-local / cloud metadata.
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("netguard: link-local address %s is blocked", ip)
	}
	if blockPrivate {
		if ip.IsLoopback() {
			return fmt.Errorf("netguard: loopback address %s is blocked by relay_block_private", ip)
		}
		if rfc1918_10.Contains(ip) || rfc1918_172.Contains(ip) || rfc1918_192.Contains(ip) {
			return fmt.Errorf("netguard: private address %s is blocked by relay_block_private", ip)
		}
		if ula.Contains(ip) {
			return fmt.Errorf("netguard: IPv6 ULA address %s is blocked by relay_block_private", ip)
		}
	}
	return nil
}
