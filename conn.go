package main

import (
	"net"
	"sync"
	"time"
)

var (
	parking = NewLockedConns()
)

// A repository of free connections to hosts. All unused connections go
// here so they can be reused (typically by the next torrent session).
// Whenever you retrieve a connection, it is removed so it can't be
// found by someone else.
type Parking struct {
	sync.Mutex
	conns map[string]*LongLivedConn
}

func NewLockedConns() Parking {
	return Parking{
		conns: make(map[string]*LongLivedConn),
	}
}

func (p Parking) Park(conn *LongLivedConn) {
	p.Lock()

	local := conn.LocalAddr().String()
	p.conns[local] = conn

	remote := conn.RemoteAddr().String()
	p.conns[remote] = conn

	conn.Timer.Reset(1 * time.Minute)
	p.Unlock()
}

func (p Parking) Retrieve(addr string) (conn *LongLivedConn, ok bool) {
	p.Lock()
	defer p.Unlock()

	if conn, ok := p.conns[addr]; ok {
		delete(p.conns, conn.LocalAddr().String())
		delete(p.conns, conn.RemoteAddr().String())

		conn.Timer.Stop()
		return conn, true
	}

	return
}

// A net.Conn that is not closed automatically but stored (indexed with
// both local and remote addresses) so it can be reused.
//
// The underlying net.Conn is automatically closed after 1 minute.
type LongLivedConn struct {
	*time.Timer
	net.Conn
}

func NewLongLivedconn(conn net.Conn) (llc *LongLivedConn) {

	llc = &LongLivedConn{
		Conn: conn,
		Timer: time.AfterFunc(1*time.Minute, func() {
			conn.Close()
		}),
	}
	llc.Timer.Stop()

	return
}

// Caller has finished using this connection. Park it.
func (llc *LongLivedConn) Close() error {
	parking.Park(llc)
	return nil
}

// Opens a conn to peer. If peer is already known and we already have a
// connection to it, it will be reused. Otherwise, open a new one.
func NewTCPConn(peer string) (conn net.Conn, err error) {
	if conn, ok := parking.Retrieve(peer); ok {
		// verify it's still open on the other side
		conn.Conn.SetReadDeadline(time.Now())
		if _, err := conn.Read([]byte{}); err == nil {
			conn.Conn.SetReadDeadline(time.Time{})
			return conn, err
		}
	}

	tcpConn, err := proxyNetDial("tcp", peer)
	if err != nil {
		return
	}

	conn = NewLongLivedconn(tcpConn)
	return
}
