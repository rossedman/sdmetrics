//go:build !linux

package scrape

import (
	"errors"
	"net"
)

func peerOf(*net.UnixConn) (Peer, error) {
	return Peer{}, errors.New("scrape: peer credentials need Linux")
}
