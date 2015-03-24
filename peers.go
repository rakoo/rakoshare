package main

import (
	"net"
	"strconv"
	"sync"
)

type Peers struct {
	sync.Mutex
	peerList []*peerState
}

func newPeers() *Peers {
	return &Peers{peerList: make([]*peerState, 0)}
}

func (lp *Peers) Know(peer, id string) bool {
	lp.Lock()
	defer lp.Unlock()

	for _, p := range lp.peerList {
		if p.id != id {
			continue
		}

		phost, _, err := net.SplitHostPort(p.address)
		if err != nil {
			return false
		}
		candidateHost, _, err := net.SplitHostPort(peer)
		if err != nil {
			return false
		}

		if phost == candidateHost {
			return true
		}
	}

	return false
}

func (lp *Peers) All() []*peerState {
	lp.Lock()
	defer lp.Unlock()

	ret := make([]*peerState, len(lp.peerList))
	for i, p := range lp.peerList {
		ret[i] = p
	}
	return ret
}

func (lp *Peers) Len() (l int) {
	lp.Lock()
	l = len(lp.peerList)
	lp.Unlock()
	return l
}

func getConnInfo(conn net.Conn) (remoteHost string, lower, upper int, ok bool) {
	_, localPort, err1 := net.SplitHostPort(conn.LocalAddr().String())
	remoteHost, remotePort, err2 := net.SplitHostPort(conn.RemoteAddr().String())

	if err1 != nil || err2 != nil {
		return
	}

	localPortInt, err3 := strconv.Atoi(localPort)
	remotePortInt, err4 := strconv.Atoi(remotePort)
	if err3 != nil || err4 != nil {
		return
	}

	if localPortInt < remotePortInt {
		return remoteHost, localPortInt, remotePortInt, true
	}
	return remoteHost, remotePortInt, localPortInt, true
}

// Add compares the new peer to be added, and adds it if it is the
// winner at our duplicate elimination algorithm. In that case we remove
// any other duplicate and return true.
func (lp *Peers) Add(peer *peerState) (keep bool) {
	thisHost, thisLower, thisUpper, ok := getConnInfo(peer.conn)
	if !ok {
		return
	}

	lp.Lock()
	defer lp.Unlock()

	toDelete := make([]int, 0)

	for i, p := range lp.peerList {
		host, lower, upper, ok := getConnInfo(p.conn)
		if !ok {
			continue
		}
		if thisHost != host {
			continue
		}

		// We already have one that's better. Keep it, and don't add the new
		// one
		if lower < thisLower || (lower == thisLower && upper < thisUpper) {
			return false
		}

		// We already have one but it should be removed and the new one be
		// added.
		toDelete = append(toDelete, i)
	}

	// Remove old ones
	for _, delIdx := range toDelete {
		lp.peerList[delIdx].Close()
		lp.peerList[delIdx] = lp.peerList[len(lp.peerList)-1]
		lp.peerList = lp.peerList[:len(lp.peerList)]
	}

	lp.peerList = append(lp.peerList, peer)
	return true
}

func (lp *Peers) Delete(peer *peerState) {
	lp.Lock()
	defer lp.Unlock()

	for i, p := range lp.peerList {
		if p.address == peer.address {
			// We don't care about the order, just put the last one here
			lp.peerList[i] = lp.peerList[len(lp.peerList)-1]
			lp.peerList = lp.peerList[:len(lp.peerList)-1]
			return
		}
	}
}

func (lp *Peers) HasPeer(peer string) bool {
	lp.Lock()
	defer lp.Unlock()

	for _, p := range lp.peerList {
		if p.address == peer {
			return true
		}
	}
	return false
}
