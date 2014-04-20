package main

import (
	"encoding/hex"
	"flag"
	"log"
	"os"
	"os/signal"
	"os/user"
	"runtime/pprof"
)

var (
	cpuprofile = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

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

	log.Println("Starting.")

	torrentSessions := make(map[string]*TorrentSession)

	// torrentDir and torrentFile
	user, err := user.Current()
	if err != nil {
		log.Fatal("Couldn't get current user: ", err)
	}
	torrentDir := filepath.Join(user.HomeDir, ".local", "share", "bitshare")
	if _, err := os.Stat(torrentDir); os.IsNotExist(err) {
		err := os.MkdirAll(torrentDir, os.ModeDir|0755)
		if err != nil {
			log.Fatal("Couldn't make home dir:", err)
		}
	}
	//torrentFile := filepath.Join(torrentDir, "current.torrent")

	// External listener
	conChan, listenPort, err := listenForPeerConnections()
	if err != nil {
		log.Fatal("Couldn't listen for peers connection: ", err)
	}

	// quitChan
	quitChan := listenSigInt()

	// DB for synchronisation
	couchdb := NewDB()

	// LPD
	lpd := &Announcer{}
	if *useLPD {
		lpd = startLPD(torrentSessions, listenPort)
	}

mainLoop:
	for {
		select {
		case <-quitChan:
			for _, ts := range torrentSessions {
				err := ts.Quit()
				if err != nil {
					log.Println("Failed: ", err)
				} else {
					log.Println("Done")
				}
			}
			break mainLoop
		case c := <-conChan:
			log.Printf("New bt connection for ih %x", c.infohash)
			if ts, ok := torrentSessions[c.infohash]; ok {
				ts.AcceptNewPeer(c)
			}
		case announce := <-lpd.announces:
			hexhash, err := hex.DecodeString(announce.infohash)
			if err != nil {
				log.Println("Err with hex-decoding:", err)
				break
			}
			if ts, ok := torrentSessions[string(hexhash)]; ok {
				log.Printf("Received LPD announce for ih %s", announce.infohash)
				ts.hintNewPeer(announce.peer)
			}
		case <-couchdb.newTorrent:
			log.Println("new torrent")
			//			for _, ts := range torrentSessions {
			//				err := ts.Quit()
			//				if err != nil {
			//					log.Println("Failed: ", err)
			//				}
			//			}
			//
			//			ts, err := NewTorrentSession(torrentFile, listenPort)
			//			if err != nil {
			//				log.Println("Could not create torrent session.", err)
			//				return
			//			}
			//			log.Printf("Starting torrent session for %x", ts.m.InfoHash)
			//			go ts.DoTorrent()
			//
			//			torrentSessions[ts.m.InfoHash] = ts
		}
	}

}

func usage() {
	log.Printf("usage: Taipei-Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}

func startLPD(torrentSessions map[string]*TorrentSession, listenPort int) (lpd *Announcer) {
	lpd, err := NewAnnouncer(listenPort)
	if err != nil {
		log.Println("Couldn't listen for Local Peer Discoveries: ", err)
		return
	} else {
		for _, ts := range torrentSessions {
			lpd.Announce(ts.m.InfoHash)
		}
	}
	return
}
