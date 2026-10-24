//go:build !linux

package scrape

import (
	"errors"
	"net"
)

// peerOf always fails: reading peer credentials needs Linux.
func peerOf(*net.UnixConn) (Peer, error) {
	return Peer{}, errors.New("scrape: peer credentials need Linux")
}
