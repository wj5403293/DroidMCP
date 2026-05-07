package main

import (
	"bufio"
	"encoding/hex"
	"net"
	"os"
	"strings"
)

// interfaceInfo describes one local NIC for the network_info tool.
type interfaceInfo struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac,omitempty"`
	Addrs []string `json:"addrs,omitempty"`
	Up    bool     `json:"up"`
}

// networkInfo is the response shape for the network_info tool.
type networkInfo struct {
	DefaultGateway string          `json:"default_gateway,omitempty"`
	DNSServers     []string        `json:"dns_servers,omitempty"`
	Interfaces     []interfaceInfo `json:"interfaces,omitempty"`
	LocalSubnet    string          `json:"local_subnet,omitempty"`
}

// procRoutePath is /proc/net/route on Linux/Termux. Tests can override it.
var procRoutePath = "/proc/net/route"

// resolvConfPath is /etc/resolv.conf. Tests can override it.
var resolvConfPath = "/etc/resolv.conf"

// gatherNetworkInfo collects the interface list, the default gateway from
// /proc/net/route and the DNS servers from /etc/resolv.conf. Best effort:
// missing files or unreadable interfaces are reflected as empty fields, not
// errors, so the caller still gets whatever we could find.
func gatherNetworkInfo() networkInfo {
	out := networkInfo{
		DefaultGateway: defaultGatewayFromProc(),
		DNSServers:     dnsServersFromResolv(),
		Interfaces:     listInterfaces(),
		LocalSubnet:    getLocalSubnet(),
	}
	return out
}

// defaultGatewayFromProc reads /proc/net/route and returns the gateway for
// the first 0.0.0.0/0 entry (lowest metric is good enough for our purpose).
// Returns "" when the file is missing or no default route is present.
func defaultGatewayFromProc() string {
	f, err := os.Open(procRoutePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		// Field 1 is destination (hex, little-endian). Default route is all zeros.
		if fields[1] != "00000000" {
			continue
		}
		// Field 2 is the gateway, also hex little-endian.
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		// /proc/net/route prints the gateway as a hex little-endian word,
		// so the bytes come out in reverse-octet order.
		return net.IPv4(raw[3], raw[2], raw[1], raw[0]).String()
	}
	return ""
}

// dnsServersFromResolv returns the nameserver lines from /etc/resolv.conf.
// On Android/Termux the file usually doesn't exist (the system uses
// `getprop net.dns1`), in which case the slice is empty.
func dnsServersFromResolv() []string {
	f, err := os.Open(resolvConfPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}

// listInterfaces enumerates local NICs with their MAC and IP addresses.
// Loopback is included so the caller can sanity-check that something is up.
func listInterfaces() []interfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]interfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		info := interfaceInfo{
			Name: iface.Name,
			MAC:  iface.HardwareAddr.String(),
			Up:   iface.Flags&net.FlagUp != 0,
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, a := range addrs {
				info.Addrs = append(info.Addrs, a.String())
			}
		}
		out = append(out, info)
	}
	return out
}

// getLocalSubnet returns the CIDR of the first non-loopback IPv4 interface,
// using the actual mask exposed by the kernel (not a hard-coded /24).
func getLocalSubnet() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		ipnet, ok := address.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			continue
		}
		network := ip4.Mask(ipnet.Mask)
		return network.String() + "/" + intToStr(ones)
	}
	return ""
}

func intToStr(i int) string {
	// Tiny helper to avoid importing strconv solely for this.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
