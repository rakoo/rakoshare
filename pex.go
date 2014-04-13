package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"time"

	bencode "code.google.com/p/bencode-go"
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

var (
	lastPeers []string
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
		newLastPeers := make([]string, 0)

		numadded := 0
		added := ""
		addedf := ""

		// TODO randomize to distribute more evenly
		for peerstring, _ := range t.peers {
			newLastPeers = append(newLastPeers, peerstring)

			if contains(lastPeers, peerstring) {
				continue
			}
			added += nettools.DottedPortToBinary(peerstring)

			// We don't manage those yet
			addedf += string(0x00)

			numadded += 1
			if numadded >= MAX_PEERS {
				break
			}
		}

		dropped := ""
		for _, peerstring := range lastPeers {
			if _, ok := t.peers[peerstring]; !ok {
				dropped += nettools.DottedPortToBinary(peerstring)
			}
		}

		for _, p := range t.peers {
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
		log.Println("Got new peer hint: ", peer)
		t.hintNewPeer(peer)
	}

	// We don't use those yet, but this is possibly how we would
	for _, flag := range stringToFlags(message.AddedF) {
		log.Printf("Got flags: %#v", flag)
	}
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

func contains(all []string, one string) bool {
	for _, maybe := range all {
		if one == maybe {
			return true
		}
	}

	return false
}
