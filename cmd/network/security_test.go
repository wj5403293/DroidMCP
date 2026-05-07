package main

import (
	"net"
	"strings"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	priv := []string{
		"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.1.1", "fe80::1", "fc00::1", "100.64.0.1", "0.0.0.0",
	}
	pub := []string{"1.1.1.1", "8.8.8.8", "203.0.113.5", "2001:4860:4860::8888"}
	for _, ip := range priv {
		if !isPrivateIP(net.ParseIP(ip)) {
			t.Errorf("expected %s to be private", ip)
		}
	}
	for _, ip := range pub {
		if isPrivateIP(net.ParseIP(ip)) {
			t.Errorf("expected %s to be public", ip)
		}
	}
}

func TestValidateTargetBlocksPublic(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	if _, err := validateTarget("1.1.1.1"); err == nil {
		t.Fatal("expected public IP to be rejected by default")
	}
}

func TestValidateTargetAllowsPrivate(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	ips, err := validateTarget("192.168.1.1")
	if err != nil {
		t.Fatalf("expected private IP to pass, got %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.168.1.1" {
		t.Errorf("unexpected resolved IPs: %v", ips)
	}
}

func TestValidateTargetOptInPublic(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "1")
	if _, err := validateTarget("1.1.1.1"); err != nil {
		t.Fatalf("expected public IP to be allowed when opted in, got %v", err)
	}
}

func TestValidateTargetEmptyHost(t *testing.T) {
	if _, err := validateTarget("   "); err == nil {
		t.Fatal("expected empty host to fail")
	}
}

func TestValidateCIDRBlocksPublic(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	if _, err := validateCIDR("1.1.1.0/24"); err == nil {
		t.Fatal("expected public CIDR to be rejected")
	}
	if !strings.Contains(mustErr(validateCIDR("1.1.1.0/24")).Error(), "private") {
		// We at least want the message to surface the policy.
		t.Errorf("expected policy mention, got %v", mustErr(validateCIDR("1.1.1.0/24")))
	}
}

func TestValidateCIDRAllowsPrivate(t *testing.T) {
	t.Setenv("DROIDMCP_NETWORK_ALLOW_PUBLIC", "")
	ipnet, err := validateCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("expected private CIDR to pass, got %v", err)
	}
	if ipnet.String() != "192.168.1.0/24" {
		t.Errorf("unexpected ipnet: %s", ipnet)
	}
}

func TestValidateCIDRRejectsBadInput(t *testing.T) {
	if _, err := validateCIDR("not a cidr"); err == nil {
		t.Fatal("expected error for malformed CIDR")
	}
}

// mustErr is a tiny helper to keep test bodies readable when we only care
// about the error.
func mustErr[T any](_ T, err error) error { return err }
