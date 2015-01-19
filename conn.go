package main

import (
	"net"

	"github.com/dchest/spipe"
)

func NewTCPConn(key []byte, peer string) (conn net.Conn, err error) {
	return spipe.Dial(key, "tcp", peer)
}
