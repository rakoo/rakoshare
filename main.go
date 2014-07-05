package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime/pprof"

	"github.com/rakoo/rakoshare/pkg/id"
)

var (
	cpuprofile = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
	idstring   = flag.String("id", "", "The id of the share")
	generate   = flag.Bool("gen", false, "If true, generate a 3-tuple of ids")
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

	if *generate {
		tmpId, err := id.New()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("WriteReadStore: %s\nReadStore: %s\nStore: %s\n",
			tmpId.WriteReadStoreID, tmpId.ReadStoreID, tmpId.StoreID)
		return
	}

	if *idstring == "" {
		fmt.Println("Missing a share id")
		usage()
		os.Exit(2)
	}
	shareID, err := id.NewFromString(*idstring)
	if err != nil {
		log.Fatal(err)
	}
	if shareID.CanWrite() {
		log.Printf("WriteReadStore: %s\n", shareID.WriteReadStoreID)
	}
	if shareID.CanRead() {
		log.Printf("ReadStore: %s\n", shareID.ReadStoreID)
	}
	log.Printf("Store: %s\n", shareID.StoreID)

	if flag.NArg() != 0 {
		log.Println("Don't want arguments")
		usage()
	}

	if *cpuprofile != "" {
		cpuf, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(cpuf)
		defer pprof.StopCPUProfile()
	}

	if *memprofile != "" {
		defer func(file string) {
			memf, err := os.Create(file)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(memf)
		}(*memprofile)
	}

	// Working directory, where all transient stuff happens
	u, err := user.Current()
	if err != nil {
		log.Fatal("Couldn't watch dir: ", err)
	}
	pathArgs := []string{u.HomeDir, ".local", "share", "rakoshare"}
	workDir := filepath.Join(pathArgs...)

	// Watcher
	if fileDir == "." {
		fmt.Println("fileDir option is missing")
		flag.Usage()
	}
	_, err = os.Stat(fileDir)
	if err != nil {
		fmt.Printf("%s is an invalid dir: %s\n", fileDir, err)
		os.Exit(1)
	}
	watcher := NewWatcher(workDir, filepath.Clean(fileDir), shareID.CanWrite())

	log.Println("Starting.")

	// External listener
	conChan, listenPort, err := listenForPeerConnections([]byte(shareID.Psk[:]))
	if err != nil {
		log.Fatal("Couldn't listen for peers connection: ", err)
	}

	var currentSession TorrentSessionI = EmptyTorrent{}

	// quitChan
	quitChan := listenSigInt()

	// LPD
	lpd := &Announcer{announces: make(chan *Announce)}
	if *useLPD {
		lpd, err = NewAnnouncer(listenPort)
		if err != nil {
			log.Fatal("Couldn't listen for Local Peer Discoveries: ", err)
		}
	}

	// Control session
	controlSession, err := NewControlSession(shareID, listenPort,
		workDir)
	if err != nil {
		log.Fatal(err)
	}
	if *useLPD {
		lpd.Announce(string(shareID.TorrentInfoHash[:]))
	}

mainLoop:
	for {
		select {
		case <-quitChan:
			err := currentSession.Quit()
			if err != nil {
				log.Println("Failed: ", err)
			} else {
				log.Println("Done")
			}
			break mainLoop
		case c := <-conChan:
			if currentSession.Matches(c.infohash) {
				currentSession.AcceptNewPeer(c)
			} else if controlSession.Matches(c.infohash) {
				controlSession.AcceptNewPeer(c)
			}
		case announce := <-lpd.announces:
			hexhash, err := hex.DecodeString(announce.infohash)
			if err != nil {
				log.Println("Err with hex-decoding:", err)
				break
			}
			if controlSession.Matches(string(hexhash)) {
				controlSession.hintNewPeer(announce.peer)
			}
		case ih := <-watcher.PingNewTorrent:
			if ih == controlSession.currentIH && !currentSession.IsEmpty() {
				break
			}

			controlSession.SetCurrent(ih)

			currentSession.Quit()

			torrentFile := filepath.Join(workDir, fmt.Sprintf("%x", ih))
			tentativeSession, err := NewTorrentSession(shareID, torrentFile, listenPort)
			if err != nil {
				log.Println("Couldn't start new session from watched dir: ", err)

				// Fallback to an emptytorrent, because the previous one is
				// invalid; hope it will be ok next time !
				currentSession = EmptyTorrent{}
				break
			}
			currentSession = tentativeSession
			go currentSession.DoTorrent()
			for _, peer := range controlSession.peers {
				currentSession.hintNewPeer(peer.address)
			}
		case announce := <-controlSession.Torrents:
			controlSession.SetCurrent(announce.infohash)

			currentSession.Quit()

			magnet := fmt.Sprintf("magnet:?xt=urn:btih:%x", announce.infohash)
			tentativeSession, err := NewTorrentSession(shareID, magnet, listenPort)
			if err != nil {
				log.Println("Couldn't start new session from announce: ", err)
				currentSession = EmptyTorrent{}
				break
			}
			currentSession = tentativeSession
			go currentSession.DoTorrent()
			currentSession.hintNewPeer(announce.peer)
		case peer := <-controlSession.NewPeers:
			if currentSession.IsEmpty() {
				magnet := fmt.Sprintf("magnet:?xt=urn:btih:%x", controlSession.currentIH)
				tentativeSession, err := NewTorrentSession(shareID, magnet, listenPort)
				if err != nil {
					log.Printf("Couldn't start new session with new peer: %s\n", err)
					break
				}
				currentSession = tentativeSession
				go currentSession.DoTorrent()
			}
			currentSession.hintNewPeer(peer)
		case meta := <-currentSession.NewMetaInfo():
			watcher.saveMetainfo(meta)
		}
	}
}

type EmptyTorrent struct{}

func (et EmptyTorrent) Quit() error                 { return nil }
func (et EmptyTorrent) Matches(ih string) bool      { return false }
func (et EmptyTorrent) AcceptNewPeer(btc *btConn)   {}
func (et EmptyTorrent) DoTorrent()                  {}
func (et EmptyTorrent) hintNewPeer(peer string)     {}
func (et EmptyTorrent) IsEmpty() bool               { return true }
func (et EmptyTorrent) NewMetaInfo() chan *MetaInfo { return nil }

func usage() {
	log.Printf("usage: Taipei-Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}
