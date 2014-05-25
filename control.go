package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/nictuku/dht"
	"github.com/zeebo/bencode"
)

var (
	// This error is returned when the incoming message is not of correct
	// type, ie EXTENSION (which is 20)
	errInvalidType = errors.New("invalid message type")
)

type ControlSession struct {
	ID     ShareID
	Port   int
	PeerID string

	Torrents chan string

	ourExtensions   map[int]string
	header          []byte
	quit            chan struct{}
	dht             *dht.DHT
	peers           map[string]*peerState
	peerMessageChan chan peerMessage
}

func NewControlSession(id ShareID, listenPort int) (*ControlSession, error) {
	sid := "-tt" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(rand.Int63(), 10)

	// TODO: UPnP UDP port mapping.
	cfg := dht.NewConfig()
	cfg.Port = listenPort
	cfg.NumTargetPeers = TARGET_NUM_PEERS

	dhtNode, err := dht.New(cfg)
	if err != nil {
		log.Fatal("DHT node creation error", err)
	}

	cs := &ControlSession{
		Port:            listenPort,
		PeerID:          sid[:20],
		ID:              id,
		Torrents:        make(chan string),
		dht:             dhtNode,
		peerMessageChan: make(chan peerMessage),
		quit:            make(chan struct{}),
		ourExtensions: map[int]string{
			1: "ut_pex",
			2: "bs_metadata",
		},
		peers: make(map[string]*peerState),
	}
	go cs.dht.Run()
	cs.dht.PeersRequest(cs.ID.PublicID(), true)

	go cs.Run()

	return cs, nil
}

func (cs *ControlSession) Header() (header []byte) {
	if len(cs.header) > 0 {
		return cs.header
	}

	header = make([]byte, 68)
	copy(header, kBitTorrentHeader[0:])
	header[27] = header[27] | 0x01
	// Support Extension Protocol (BEP-0010)
	header[25] |= 0x10

	binID, err := hex.DecodeString(cs.ID.PublicID())
	if err != nil {
		log.Fatal(err)
	}

	copy(header[28:48], []byte(binID))
	copy(header[48:68], []byte(cs.PeerID))

	cs.header = header

	return
}

func (cs *ControlSession) deadlockDetector(heartbeat, quit chan struct{}) {
	lastHeartbeat := time.Now()

deadlockLoop:
	for {
		select {
		case <-quit:
			break deadlockLoop
		case <-heartbeat:
			lastHeartbeat = time.Now()
		case <-time.After(15 * time.Second):
			age := time.Now().Sub(lastHeartbeat)
			log.Println("Starvation or deadlock of main thread detected. Look in the stack dump for what Run() is currently doing.")
			log.Println("Last heartbeat", age.Seconds(), "seconds ago")
			panic("Killed by deadlock detector")
		}
	}
}
func (cs *ControlSession) Run() {
	// deadlock
	heartbeat := make(chan struct{}, 1)
	quitDeadlock := make(chan struct{})
	go cs.deadlockDetector(heartbeat, quitDeadlock)

	rechokeChan := time.Tick(1 * time.Second)
	verboseChan := time.Tick(10 * time.Second)
	keepAliveChan := time.Tick(60 * time.Second)

	// Start out polling tracker every 20 seconds until we get a response.
	// Maybe be exponential backoff here?
	var retrackerChan <-chan time.Time
	retrackerChan = time.Tick(20 * time.Second)
	trackerInfoChan := make(chan *TrackerResponse)
	trackerReportChan := make(chan ClientStatusReport)
	startTrackerClient("", [][]string{}, trackerInfoChan, trackerReportChan)

	trackerReportChan <- cs.makeClientStatusReport("started")

	log.Println("[CONTROL] Start")

	for {
		select {
		case <-retrackerChan:
			trackerReportChan <- cs.makeClientStatusReport("")
		case dhtInfoHashPeers := <-cs.dht.PeersRequestResults:
			newPeerCount := 0
			// key = infoHash. The torrent client currently only
			// supports one download at a time, so let's assume
			// it's the case.
			for _, peers := range dhtInfoHashPeers {
				for _, peer := range peers {
					peer = dht.DecodePeerAddress(peer)
					if _, ok := cs.peers[peer]; !ok {
						newPeerCount++
						go cs.connectToPeer(peer)
					}
				}
			}
			// log.Println("Contacting", newPeerCount, "new peers (thanks DHT!)")
		case ti := <-trackerInfoChan:
			newPeerCount := 0
			for _, peer := range ti.Peers {
				if _, ok := cs.peers[peer]; !ok {
					newPeerCount++
					go cs.connectToPeer(peer)
				}
			}
			for _, peer6 := range ti.Peers6 {
				if _, ok := cs.peers[peer6]; !ok {
					newPeerCount++
					go cs.connectToPeer(peer6)
				}
			}

			log.Println("Contacting", newPeerCount, "new peers")
			interval := ti.Interval
			if interval < 120 {
				interval = 120
			} else if interval > 24*3600 {
				interval = 24 * 3600
			}
			log.Println("..checking again in", interval, "seconds.")
			retrackerChan = time.Tick(interval * time.Second)
			log.Println("Contacting", newPeerCount, "new peers")

		case pm := <-cs.peerMessageChan:
			peer, message := pm.peer, pm.message
			peer.lastReadTime = time.Now()
			log.Printf("Received message from %s\n", peer.address)
			err2 := cs.DoMessage(peer, message)
			if err2 != nil {
				if err2 != io.EOF {
					log.Println("Closing peer", peer.address, "because", err2)
				}
				cs.ClosePeer(peer)
			}
		case <-rechokeChan:
			// TODO: recalculate who to choke / unchoke
			heartbeat <- struct{}{}
			if len(cs.peers) < TARGET_NUM_PEERS {
				go cs.dht.PeersRequest(cs.ID.PublicID(), true)
				trackerReportChan <- cs.makeClientStatusReport("")
			}
		case <-verboseChan:
			log.Println("[CONTROL] Peers:", len(cs.peers))
		case <-keepAliveChan:
			now := time.Now()
			for _, peer := range cs.peers {
				if peer.lastReadTime.Second() != 0 && now.Sub(peer.lastReadTime) > 3*time.Minute {
					// log.Println("Closing peer", peer.address, "because timed out.")
					cs.ClosePeer(peer)
					continue
				}
				peer.keepAlive(now)
			}

		case <-cs.quit:
			log.Println("Quitting torrent session")
			quitDeadlock <- struct{}{}
			return
		}
	}

}

func (cs *ControlSession) Quit() error {
	cs.quit <- struct{}{}
	for _, peer := range cs.peers {
		cs.ClosePeer(peer)
	}
	if cs.dht != nil {
		cs.dht.Stop()
	}
	return nil
}

func (cs *ControlSession) makeClientStatusReport(event string) ClientStatusReport {
	return ClientStatusReport{
		Event:    event,
		InfoHash: cs.ID.PublicID(),
		PeerId:   cs.PeerID,
		Port:     cs.Port,
	}
}

func (cs *ControlSession) connectToPeer(peer string) {
	conn, err := proxyNetDial("tcp", peer)
	if err != nil {
		// log.Println("Failed to connect to", peer, err)
		return
	}

	header := cs.Header()
	_, err = conn.Write(header)
	if err != nil {
		log.Println("Failed to send header to", peer, err)
		return
	}

	theirheader, err := readHeader(conn)
	if err != nil {
		log.Printf("Failed to read header from %s: %s\n", peer, err)
		return
	}

	peersInfoHash := string(theirheader[8:28])
	id := string(theirheader[28:48])

	// If it's us, we don't need to continue
	if id == cs.PeerID {
		log.Println("Tried to connecting to ourselves. Closing.")
		conn.Close()
		return
	}

	btconn := &btConn{
		header:   theirheader,
		infohash: peersInfoHash,
		id:       id,
		conn:     conn,
	}
	// log.Println("Connected to", peer)
	cs.AddPeer(btconn)
}

func (cs *ControlSession) hintNewPeer(peer string) {
	if _, ok := cs.peers[peer]; !ok {
		go cs.connectToPeer(peer)
	}
}

func (cs *ControlSession) AcceptNewPeer(btconn *btConn) {
	_, err := btconn.conn.Write(cs.Header())
	if err != nil {
		log.Printf("Error writing header: %s\n", err)
		return
	}
	cs.AddPeer(btconn)
}

func (cs *ControlSession) AddPeer(btconn *btConn) {
	for _, p := range cs.peers {
		if p.id == btconn.id {
			return
		}
	}

	theirheader := btconn.header

	peer := btconn.conn.RemoteAddr().String()
	// log.Println("Adding peer", peer)
	if len(cs.peers) >= MAX_NUM_PEERS {
		log.Println("We have enough peers. Rejecting additional peer", peer)
		btconn.conn.Close()
		return
	}
	ps := NewPeerState(btconn.conn)
	ps.address = peer
	ps.id = btconn.id
	// If 128, then it supports DHT.
	if int(theirheader[7])&0x01 == 0x01 {
		// It's OK if we know this node already. The DHT engine will
		// ignore it accordingly.
		go cs.dht.AddNode(ps.address)
	}

	cs.peers[peer] = ps
	go ps.peerWriter(cs.peerMessageChan)
	go ps.peerReader(cs.peerMessageChan)

	if int(theirheader[5])&0x10 == 0x10 {
		ps.SendExtensions(cs.ourExtensions, 0)
	}
}

func (cs *ControlSession) ClosePeer(peer *peerState) {
	log.Println("Closing peer", peer.address)
	peer.Close()
	delete(cs.peers, peer.address)
}

func (cs *ControlSession) DoMessage(p *peerState, message []byte) (err error) {
	if message == nil {
		return io.EOF // The reader or writer goroutine has exited
	}
	if len(message) == 0 { // keep alive
		return
	}

	if message[0] != EXTENSION {
		return errInvalidType
	}
	switch message[1] {
	case EXTENSION_HANDSHAKE:
		err = cs.DoHandshake(message[1:], p)
	default:
		err = cs.DoOther(message[1:], p)
	}

	return
}

func (cs *ControlSession) DoHandshake(msg []byte, p *peerState) (err error) {
	var h ExtensionHandshake
	err = bencode.NewDecoder(bytes.NewReader(msg[1:])).Decode(&h)
	if err != nil {
		log.Println("Error when unmarshaling extension handshake")
		return err
	}

	p.theirExtensions = make(map[string]int)
	for name, code := range h.M {
		p.theirExtensions[name] = code
	}
	return
}

func (cs *ControlSession) DoOther(msg []byte, p *peerState) (err error) {
	if ext, ok := cs.ourExtensions[int(msg[0])]; ok {
		switch ext {
		case "bs_metadata":
			err = cs.DoMetadata(msg[1:], p)
		case "ut_pex":
			err = cs.DoPex(msg[1:], p)
		default:
			err = errors.New(fmt.Sprintf("unknown extension: %s", ext))
		}
	} else {
		err = errors.New(fmt.Sprintf("Unknown extension: %d", int(msg[0])))
	}

	return
}

type MetaMessage struct {
	InfoHash string `bencode:"infohash"`
}

func (cs *ControlSession) DoMetadata(msg []byte, p *peerState) (err error) {
	var message MetaMessage
	err = bencode.NewDecoder(bytes.NewReader(msg)).Decode(&message)
	if err != nil {
		return
	}
	if message.InfoHash == "" {
		return
	}

	cs.Torrents <- message.InfoHash
	return
}

func (cs *ControlSession) DoPex(msg []byte, p *peerState) (err error) {
	return
}

func (cs *ControlSession) Matches(ih string) bool {
	return cs.ID.PublicID() == hex.EncodeToString([]byte(ih))
}

func (cs *ControlSession) Broadcast(ih string) {
	for _, ps := range cs.peers {
		if _, ok := ps.theirExtensions["bs_metadata"]; !ok {
			continue
		}

		var resp bytes.Buffer
		resp.WriteByte(EXTENSION)
		resp.WriteByte(byte(ps.theirExtensions["bs_metadata"]))

		msg := MetaMessage{InfoHash: ih}
		err := bencode.NewEncoder(&resp).Encode(msg)
		if err != nil {
			log.Println("Couldn't encode msg: ", err)
			continue
		}

		ps.sendMessage(resp.Bytes())
	}
}
