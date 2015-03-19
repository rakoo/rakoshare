package main

import (
	"log"
	"net"

	"github.com/dchest/spipe"
)

func NewTCPConn(key []byte, peer string) (conn net.Conn, err error) {
	conn, err = spipe.Dial(key, "tcp", peer)
	if err == nil {
		log.Println("[CONN] New connection to", peer)
	}
	return
}
