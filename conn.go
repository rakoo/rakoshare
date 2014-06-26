package main

import (
	"net"

	"github.com/dchest/spipe"
)

// Opens a spiped conn to peer. If peer is already known and we already have a
// connection to it, it will be reused. Otherwise, open a new one.
func NewTCPConn(key []byte, peer string) (conn net.Conn, err error) {
	return spipe.Dial(key, "tcp", peer)
}
