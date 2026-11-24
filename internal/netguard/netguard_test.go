package netguard

import (
	"net"
	"testing"
)

// D-08: relay targets are registered by remote parties, so their addresses
// must be policy-checked. Default policy blocks only link-local/cloud
// metadata ranges; loopback and RFC1918 stay allowed so local relay clusters
// keep working. Strict mode (relay_block_private) additionally blocks
// loopback, RFC1918 and IPv6 ULA.

func TestCheckIP_DefaultPolicy(t *testing.T) {
	blocked := []string{
		"169.254.169.254",        // cloud metadata
		"169.254.1.1",            // link-local v4
		"fe80::1",                // link-local v6
		"::ffff:169.254.169.254", // IPv4-mapped metadata
	}
	for _, s := range blocked {
		if err := CheckIP(net.ParseIP(s), false); err == nil {
			t.Errorf("default policy should block %s", s)
		}
	}
	allowed := []string{
		"127.0.0.1", "8.8.8.8", "10.0.0.1", "192.168.1.5", "172.16.0.1",
		"::1", "2001:db8::1",
	}
	for _, s := range allowed {
		if err := CheckIP(net.ParseIP(s), false); err != nil {
			t.Errorf("default policy should allow %s, got %v", s, err)
		}
	}
}

func TestCheckIP_StrictPolicy(t *testing.T) {
	blocked := []string{
		"169.254.169.254", "fe80::1",
		"127.0.0.1", "::1",
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.0.1",
		"fc00::1", "fd12:3456::1",
		"::ffff:127.0.0.1", // IPv4-mapped loopback
		"::ffff:10.0.0.1",  // IPv4-mapped RFC1918
	}
	for _, s := range blocked {
		if err := CheckIP(net.ParseIP(s), true); err == nil {
			t.Errorf("strict policy should block %s", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "2001:db8::1", "172.32.0.1"}
	for _, s := range allowed {
		if err := CheckIP(net.ParseIP(s), true); err != nil {
			t.Errorf("strict policy should allow public %s, got %v", s, err)
		}
	}
}

func TestCheckIP_NilFailsClosed(t *testing.T) {
	if err := CheckIP(nil, false); err == nil {
		t.Error("nil IP must be rejected (fail closed)")
	}
}
