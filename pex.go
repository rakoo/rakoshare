package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"time"

	bencode "github.com/jackpal/bencode-go"
	"github.com/nictuku/nettools"
)

const (
	PEER_LEN  = 6
	MAX_PEERS = 50
)

const (
	SUPPORTS_ENCRYPTION byte = 1 << iota
	IS_SEED
	SUPPORTS_UTP
	SUPPORTS_HOLE_PUNCHING
)

type pexPeer struct {
	address string
	id      string
}

var (
	lastPeers []pexPeer
)

type Flag struct {
	SupportsEncryption   bool
	IsSeed               bool
	SupportsHolePunching bool

	// This one is a positive flag: if true, it's true. If false, it may
	// still be true
	SupportsUTP bool
}

// As specified in libtorrent source: see
// http://sourceforge.net/p/libtorrent/code/HEAD/tree/trunk/src/ut_pex.cpp#l554
func NewFlag(b byte) (f *Flag) {
	f = &Flag{
		SupportsEncryption:   match(b, SUPPORTS_ENCRYPTION),
		IsSeed:               match(b, IS_SEED),
		SupportsUTP:          match(b, SUPPORTS_UTP),
		SupportsHolePunching: match(b, SUPPORTS_HOLE_PUNCHING),
	}
	return
}

func match(b byte, feature byte) bool {
	return b&feature == feature
}

type PexMessage struct {
	Added   string "added"
	AddedF  string "added.f"
	Dropped string "dropped"
}

// The main loop.
// Every minute, we calculate the new pex message and send it to
// everyone. After it's sent, this new pex message becomes the last
// pex message.
//
// A Pex Message has three fields: "added", "addedf" and "dropped".
//
// "added" represents the set of peers we are currently connected to
// that we weren't connected to last time.
//
// "addedf" contains the same peers but instead of their address, it's
// their flags (See the Flag structure for more details)
//
// "dropped" is the set of peers we were connected to last time that
// we aren't currently connected to.
func (t *TorrentSession) StartPex() {
	for _ = range time.Tick(1 * time.Minute) {
		newLastPeers := make([]pexPeer, 0)

		numadded := 0
		added := ""
		addedf := ""

		// TODO randomize to distribute more evenly
		for _, peer := range t.peers.All() {
			newLastPeers = append(newLastPeers, pexPeer{peer.address, peer.id})

			if contains(lastPeers, peer) {
				continue
			}
			added += nettools.DottedPortToBinary(peer.address)

			// We don't manage those yet
			addedf += string(0x00)

			numadded += 1
			if numadded >= MAX_PEERS {
				break
			}
		}

		dropped := ""
		for _, lastPeer := range lastPeers {
			if !t.peers.Know(lastPeer.address, lastPeer.id) {
				dropped += nettools.DottedPortToBinary(lastPeer.address)
			}
		}

		for _, p := range t.peers.All() {
			p.sendExtensionMessage("ut_pex", PexMessage{
				Added:   added,
				AddedF:  addedf,
				Dropped: dropped,
			})
		}

		lastPeers = newLastPeers
	}
}

func (t *TorrentSession) DoPex(msg []byte, p *peerState) {
	var message PexMessage
	err := bencode.Unmarshal(bytes.NewReader(msg), &message)
	if err != nil {
		log.Println("Error when parsing pex: ", err)
		return
	}

	for _, peer := range stringToPeers(message.Added) {
		t.hintNewPeer(peer)
	}

	// We don't use those yet, but this is possibly how we would
	//for _, flag := range stringToFlags(message.AddedF) {
	//log.Printf("Got flags: %#v", flag)
	//}
}

func stringToPeers(in string) (peers []string) {
	peers = make([]string, 0)
	for i := 0; i < len(in); i += PEER_LEN {
		peer := nettools.BinaryToDottedPort(in[i : i+PEER_LEN])
		peers = append(peers, peer)
	}

	return
}

func stringToFlags(in string) (flags []*Flag) {
	flags = make([]*Flag, 0)
	rd := bytes.NewReader([]byte(in))
	for {
		var flag uint8
		err := binary.Read(rd, binary.BigEndian, &flag)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				log.Println("Couldn't decode flag into uint8: ", err)
			}
		}
		flags = append(flags, NewFlag(flag))
	}

	return
}

func contains(all []pexPeer, peer *peerState) bool {
	for _, maybe := range all {
		if peer.id == maybe.id && peer.address == maybe.address {
			return true
		}
	}

	return false
}
