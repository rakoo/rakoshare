package main

import (
	"net"
	"time"

	"github.com/dchest/spipe"
)

type BufferedSpipeConn struct {
	net.Conn
	packets chan []byte
	quit    chan struct{}
}

func newBufferedSpipeConn(conn net.Conn) BufferedSpipeConn {
	bsc := BufferedSpipeConn{conn, make(chan []byte), make(chan struct{}, 1)}

	go func() {
		var buf [1024]byte
		buflen := 0

		// experimentally, the callers don't batch faster than every
		// 10ms, so 50ms is a safe amount of time to wait
		tick := time.Tick(50 * time.Millisecond)

		for {
			select {
			case packet := <-bsc.packets:
				if buflen+len(packet) < len(buf) {
					n := copy(buf[buflen:], packet)
					buflen += n
					break
				}

				copy(buf[buflen:], packet[:1024-buflen])
				conn.Write(buf[:])

				packet = packet[1024-buflen:]
				for len(packet) > 1024 {
					conn.Write(packet[:1024])
					packet = packet[1024:]
				}

				buflen = copy(buf[:], packet)
			case <-tick:
				if buflen == 0 {
					break
				}

				conn.Write(buf[:buflen])
				buflen = 0
			case <-bsc.quit:
				return
			}
		}
	}()

	return bsc
}

func (bsc BufferedSpipeConn) Write(p []byte) (n int, err error) {
	bsc.packets <- p

	// MEH
	return len(p), nil
}

func (bsc BufferedSpipeConn) Close() error {
	bsc.quit <- struct{}{}
	return bsc.Conn.Close()
}

func NewTCPConn(key []byte, peer string) (conn net.Conn, err error) {
	sconn, err := spipe.Dial(key, "tcp", peer)
	if err != nil {
		return
	}

	return newBufferedSpipeConn(sconn), nil
}
