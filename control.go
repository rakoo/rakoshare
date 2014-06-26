package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ed "github.com/agl/ed25519"
	"github.com/nictuku/dht"
	"github.com/rakoo/rakoshare/pkg/id"
	"github.com/zeebo/bencode"
)

var (
	// This error is returned when the incoming message is not of correct
	// type, ie EXTENSION (which is 20)
	errInvalidType = errors.New("invalid message type")
)

var (
	errMetadataMessage = errors.New("Couldn't create metadata message")
)

type ControlSession struct {
	ID     *id.Id
	Port   int
	PeerID string

	// A channel of all announces we get from peers.
	// If the announce is for the same torrent as the current one, then it
	// is not broadcasted in this channel.
	Torrents chan Announce

	// A channel of all new peers we acknowledge, in a ip:port format
	// The port is the one advertised
	NewPeers chan string

	// The current data torrent
	currentIH string
	rev       string

	ourExtensions   map[int]string
	header          []byte
	quit            chan struct{}
	dht             *dht.DHT
	peers           map[string]*peerState
	peerMessageChan chan peerMessage

	workDir string
}

func NewControlSession(shareid *id.Id, listenPort int, workDir string) (*ControlSession, error) {
	sid := "-tt" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(rand.Int63(), 10)

	// TODO: UPnP UDP port mapping.
	cfg := dht.NewConfig()
	cfg.Port = listenPort
	cfg.NumTargetPeers = TARGET_NUM_PEERS

	dhtNode, err := dht.New(cfg)
	if err != nil {
		log.Fatal("DHT node creation error", err)
	}

	current, err := os.Open(path.Join(workDir, "current"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var currentIhMessage IHMessage
	err = bencode.NewDecoder(current).Decode(&currentIhMessage)
	if err != nil {
		log.Printf("Couldn't decode current message, starting from scratch: %s\n", err)
	}

	rev := "0-"
	if currentIhMessage.Info.Rev != "" {
		parts := strings.Split(currentIhMessage.Info.Rev, "2")
		if len(parts) == 2 {
			if _, err := strconv.Atoi(parts[0]); err == nil {
				rev = currentIhMessage.Info.Rev
			}
		}
	}

	cs := &ControlSession{
		Port:            listenPort,
		PeerID:          sid[:20],
		ID:              shareid,
		Torrents:        make(chan Announce),
		NewPeers:        make(chan string),
		dht:             dhtNode,
		peerMessageChan: make(chan peerMessage),
		quit:            make(chan struct{}),
		ourExtensions: map[int]string{
			1: "ut_pex",
			2: "bs_metadata",
		},
		peers: make(map[string]*peerState),

		currentIH: currentIhMessage.Info.InfoHash,
		rev:       rev,

		workDir: workDir,
	}
	go cs.dht.Run()
	cs.dht.PeersRequest(string(cs.ID.TorrentInfoHash[:]), true)

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

	copy(header[28:48], cs.ID.TorrentInfoHash[:])
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
	verboseChan := time.Tick(10 * time.Minute)
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
				go cs.dht.PeersRequest(string(cs.ID.TorrentInfoHash[:]), true)
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
		InfoHash: string(cs.ID.TorrentInfoHash[:]),
		PeerId:   cs.PeerID,
		Port:     cs.Port,
	}
}

func (cs *ControlSession) connectToPeer(peer string) {
	conn, err := NewTCPConn(peer)
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

	cs.NewPeers <- peer
}

func (cs *ControlSession) hintNewPeer(peer string) {
	if _, ok := cs.peers[peer]; !ok {
		go cs.connectToPeer(peer)
	}
}

func (cs *ControlSession) AcceptNewPeer(btconn *btConn) {
	// If it's us, we don't need to continue
	if btconn.id == cs.PeerID {
		btconn.conn.Close()
		return
	}

	_, err := btconn.conn.Write(cs.Header())
	if err != nil {
		log.Printf("Error writing header: %s\n", err)
		btconn.conn.Close()
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

	// Now that handshake is done and we know their extension, send the
	// current ih message
	message, err := NewIHMessage(int64(cs.Port), cs.currentIH, cs.rev, cs.ID.Priv)
	if err != nil {
		log.Println(err)
	} else {
		p.sendExtensionMessage("bs_metadata", message)
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

type IHMessage struct {
	Info NewInfo `bencode:"info"`

	// The port we are listening on
	Port int64 `bencode:"port"`

	// The signature of the info dict
	Sig string `bencode:"sig"`
}

type NewInfo struct {
	InfoHash string `bencode:"infohash"`

	// The revision, ala CouchDB
	// ie <counter>-<hash>
	Rev string `bencode:"rev"`
}

func NewIHMessage(port int64, ih, rev string, priv id.PrivKey) (mm IHMessage, err error) {
	mm = IHMessage{
		Info: NewInfo{
			InfoHash: ih,
			Rev:      rev,
		},
		Port: port,
	}

	var buf bytes.Buffer
	err = bencode.NewEncoder(&buf).Encode(mm.Info)
	if err != nil {
		log.Printf("[CONTROL] Couldn't encode ih message, returning now")
		return mm, err
	}

	privarg := new([ed.PrivateKeySize]byte)
	copy(privarg[:], priv[:])

	sig := ed.Sign(privarg, buf.Bytes())
	mm.Sig = string(sig[:])

	return
}

func (cs *ControlSession) DoMetadata(msg []byte, p *peerState) (err error) {
	var message IHMessage
	err = bencode.NewDecoder(bytes.NewReader(msg)).Decode(&message)
	if err != nil {
		return
	}
	if message.Info.InfoHash == "" || message.Port == 0 {
		return
	}

	if cs.isNewerThan(message.Info.Rev) {
		return
	}

	// take his IP addr, use the advertised port
	ip := p.conn.RemoteAddr().(*net.TCPAddr).IP.String()
	port := strconv.Itoa(int(message.Port))
	peer := ip + ":" + port

	go func() {
		cs.NewPeers <- peer
	}()

	if cs.currentIH != message.Info.InfoHash {

		var tmpInfoBuf bytes.Buffer
		err = bencode.NewEncoder(&tmpInfoBuf).Encode(message.Info)
		if err != nil {
			log.Printf("[CONTROL] Couldn't encode ih message, returning now")
			return err
		}

		pub := [ed.PublicKeySize]byte(cs.ID.Pub)
		sig := new([ed.SignatureSize]byte)
		copy(sig[:], []byte(message.Sig))
		ok := ed.Verify(&pub, tmpInfoBuf.Bytes(), sig)
		if !ok {
			log.Printf("[CONTROL] Bad signature")
			return errors.New("Bad Signature")
		}

		cs.Torrents <- Announce{
			infohash: message.Info.InfoHash,
			peer:     peer,
		}
	}
	return
}

func (cs *ControlSession) isNewerThan(rev string) bool {
	remoteParts := strings.Split(rev, "-")
	if len(remoteParts) != 2 {
		return true
	}
	remoteCounter, err := strconv.Atoi(remoteParts[0])
	if err != nil {
		return true
	}

	localParts := strings.Split(cs.rev, "-")
	if len(localParts) != 2 {
		return true
	}
	localCounter, err := strconv.Atoi(localParts[0])
	if err != nil {
		return true
	}

	return localCounter > remoteCounter
}

func (cs *ControlSession) DoPex(msg []byte, p *peerState) (err error) {
	return
}

func (cs *ControlSession) Matches(ih string) bool {
	return string(cs.ID.TorrentInfoHash[:]) == ih
}

func (cs *ControlSession) SetCurrent(ih string) {
	cs.currentIH = ih

	parts := strings.Split(cs.rev, "-")
	if len(parts) != 2 {
		log.Printf("Invalid rev: %s\n", cs.rev)
		parts = []string{"0", ""}
	}

	counter, err := strconv.Atoi(parts[0])
	if err != nil {
		counter = 0
	}
	newCounter := strconv.Itoa(counter + 1)

	cs.rev = newCounter + "-" + fmt.Sprintf("%x", sha1.Sum([]byte(ih+parts[1])))

	// Overwrite "current" file with current value
	currentFile, err := os.Create(filepath.Join(cs.workDir, "current"))
	if err != nil {
		log.Fatal(err)
	}
	defer currentFile.Close()

	mess, err := NewIHMessage(int64(cs.Port), cs.currentIH, cs.rev,
		cs.ID.Priv)
	if err != nil {
		return
	}
	err = bencode.NewEncoder(currentFile).Encode(mess)

	cs.broadcast(ih)
}

func (cs *ControlSession) broadcast(ih string) {
	for _, ps := range cs.peers {
		if _, ok := ps.theirExtensions["bs_metadata"]; !ok {
			continue
		}

		message, err := NewIHMessage(int64(cs.Port), ih, cs.rev, cs.ID.Priv)
		if err != nil {
			log.Println(err)
		} else {
			ps.sendExtensionMessage("bs_metadata", message)
		}
	}
}
