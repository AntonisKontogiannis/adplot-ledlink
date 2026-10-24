//go:build !linux

package colorlight

import "errors"

// openRawSender is only implemented for Linux (AF_PACKET) so far. Windows
// would need an Npcap-based transport; run LED Link on Linux for Colorlight
// output, or drive the panels through the DDP path with an open receiver.
func openRawSender(iface string) (frameSender, error) {
	return nil, errors.New("colorlight raw Ethernet output is Linux-only for now")
}
