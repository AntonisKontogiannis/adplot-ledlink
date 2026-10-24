// Package discovery expands scan targets (single hosts, CIDR ranges and the
// local subnet) into a flat list of hosts LED Link can probe.
package discovery

import (
	"fmt"
	"net"
	"strings"
)

// DefaultMaxHosts caps how many addresses one scan may probe.
const DefaultMaxHosts = 1024

// Expand turns a target spec into a list of hosts.
//
// Supported specs:
//   - "local" or "" — the local machine's IPv4 subnet(s)
//   - "host" or "host,host2,..." — literal hosts/IPs
//   - "192.168.1.0/24" — CIDR ranges
//
// Modes can be mixed: "local,10.0.0.5". Duplicates are removed.
func Expand(spec string, maxHosts int) ([]string, error) {
	if maxHosts <= 0 {
		maxHosts = DefaultMaxHosts
	}
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "local") {
		return LocalHosts(maxHosts)
	}

	var (
		hosts []string
		seen  = make(map[string]bool)
	)
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		hosts = append(hosts, h)
	}

	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			continue
		case strings.EqualFold(part, "local"):
			locals, err := LocalHosts(maxHosts)
			if err != nil {
				return nil, err
			}
			for _, h := range locals {
				add(h)
			}
		case strings.Contains(part, "/"):
			ips, err := expandCIDR(part, maxHosts)
			if err != nil {
				return nil, err
			}
			for _, h := range ips {
				add(h)
			}
		default:
			add(part)
		}
		if len(hosts) >= maxHosts {
			return hosts[:maxHosts], nil
		}
	}
	return hosts, nil
}

// LocalHosts lists the probeable hosts of the local IPv4 subnet(s).
// Home and office networks usually use /24; larger subnets are narrowed to
// the /24 containing the interface address to keep scans fast.
func LocalHosts(maxHosts int) ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}

	var (
		hosts []string
		seen  = make(map[string]bool)
	)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLinkLocalUnicast() {
				continue
			}
			cidr := ipnet
			if ones, bits := ipnet.Mask.Size(); bits == 32 && ones < 24 {
				mask := net.CIDRMask(24, 32)
				cidr = &net.IPNet{IP: ip4.Mask(mask), Mask: mask}
			}
			ips, err := expandCIDR(cidr.String(), maxHosts)
			if err != nil {
				continue
			}
			for _, h := range ips {
				if seen[h] {
					continue
				}
				seen[h] = true
				hosts = append(hosts, h)
				if len(hosts) >= maxHosts {
					return hosts, nil
				}
			}
		}
	}
	return hosts, nil
}

// expandCIDR lists the usable host addresses of an IPv4 CIDR range.
// Network and broadcast addresses are skipped except for /31 and /32.
func expandCIDR(cidr string, maxHosts int) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("only IPv4 ranges are supported, got %q", cidr)
	}

	base := ip4.Mask(ipnet.Mask)
	broadcast := make(net.IP, len(base))
	for i := range base {
		broadcast[i] = base[i] | ^ipnet.Mask[i]
	}
	ones, _ := ipnet.Mask.Size()

	current := make(net.IP, len(base))
	copy(current, base)

	var out []string
	for ipnet.Contains(current) {
		usable := ones >= 31 || (!current.Equal(base) && !current.Equal(broadcast))
		if usable {
			out = append(out, current.String())
			if len(out) >= maxHosts {
				break
			}
		}
		if !inc(current) {
			break
		}
	}
	return out, nil
}

// inc increments an IP address in place, returning false on overflow.
func inc(ip net.IP) bool {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return true
		}
	}
	return false
}
