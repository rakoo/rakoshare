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
)

var (
	cpuprofile = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
	id         = flag.String("id", "", "The id of the share")
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

	if *id == "" {
		fmt.Println("Missing a share id")
		usage()
		os.Exit(2)
	}
	shareID := NewShareID(*id)

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

	// Bitshare dir
	u, err := user.Current()
	if err != nil {
		log.Fatal("Couldn't watch dir: ", err)
	}
	pathArgs := []string{u.HomeDir, ".local", "share", "bitshare"}
	bitshareDir := filepath.Join(pathArgs...)

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
	watcher := NewWatcher(bitshareDir, filepath.Clean(fileDir))

	log.Println("Starting.")

	// External listener
	conChan, listenPort, err := listenForPeerConnections()
	if err != nil {
		log.Fatal("Couldn't listen for peers connection: ", err)
	}

	var currentSession *TorrentSession

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
	controlSession, err := NewControlSession(shareID, listenPort)
	if err != nil {
		log.Fatal(err)
	}
	id, err := hex.DecodeString(shareID.PublicID())
	if err != nil {
		log.Fatal(err)
	}
	if *useLPD {
		lpd.Announce(string(id))
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
			if currentSession.Matches(string(hexhash)) {
				currentSession.hintNewPeer(announce.peer)
			} else if controlSession.Matches(string(hexhash)) {
				controlSession.hintNewPeer(announce.peer)
			}
		case ih := <-watcher.PingNewTorrent:
			controlSession.Broadcast(ih)

			torrentFile := filepath.Join(bitshareDir, fmt.Sprintf("%x", ih))
			currentSession, err = NewTorrentSession(torrentFile, listenPort)
			if err != nil {
				log.Fatal("Couldn't start new session: ", err)
			}
			go currentSession.DoTorrent()
			if *useLPD {
				lpd.Announce(ih)
			}
		case ih := <-controlSession.Torrents:
			log.Printf("Got new metadata from control session: %x\n", ih)
		}
	}

}

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
