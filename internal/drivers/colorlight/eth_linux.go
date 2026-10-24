//go:build linux

package colorlight

import (
	"fmt"
	"net"
	"syscall"
)

// rawSender transmits raw Ethernet frames on one interface via AF_PACKET.
// Requires root or CAP_NET_RAW (e.g. `sudo setcap cap_net_raw+ep ./ledlink`).
type rawSender struct {
	fd      int
	ifindex int
}

// openRawSender opens an AF_PACKET socket bound to iface.
func openRawSender(iface string) (frameSender, error) {
	ifaceObj, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("colorlight interface %q: %w (list interfaces with `ip link`)", iface, err)
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("open raw socket for %s (need root or CAP_NET_RAW): %w", iface, err)
	}
	addr := &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_ALL), Ifindex: ifaceObj.Index}
	if err := syscall.Bind(fd, addr); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind raw socket to %s: %w", iface, err)
	}
	return &rawSender{fd: fd, ifindex: ifaceObj.Index}, nil
}

// Send implements frameSender.
func (s *rawSender) Send(frame []byte) error {
	return syscall.Sendto(s.fd, frame, 0, &syscall.SockaddrLinklayer{Ifindex: s.ifindex})
}

// Close implements frameSender.
func (s *rawSender) Close() error { return syscall.Close(s.fd) }

// htons converts a uint16 to network byte order.
func htons(v uint16) uint16 { return v<<8 | v>>8 }
