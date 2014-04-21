package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io/ioutil"
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
		case magnet := <-couchdb.newTorrent:
			log.Println("new torrent:", magnet)

			ts, err := startSession(magnet, torrentSessions, listenPort, lpd)
			if err != nil {
				log.Fatal("Couldn't start new session: ", err)
			}

			go func() {
				for meta := range ts.NewTorrent {
					meta.saveToDisk(bitshareDir)

					currentFile := filepath.Join(bitshareDir, "current")
					ihhex := fmt.Sprintf("%x", meta.InfoHash)
					ioutil.WriteFile(currentFile, []byte(ihhex), 0600)
				}
			}()

			torrentSessions[ts.m.InfoHash] = ts
		case ih := <-watcher.PingNewTorrent:
			couchdb.PushNewTorrent(ih)

			torrentFile := filepath.Join(bitshareDir, fmt.Sprintf("%x", ih))
			startSession(torrentFile, torrentSessions, listenPort, lpd)
		}
	}

}

func startSession(torrent string, torrentSessions map[string]*TorrentSession, listenPort int, lpd *Announcer) (ts *TorrentSession, err error) {

	meta, err := NewMetaInfo(torrent)
	if err != nil {
		return
	}
	if ts, ok := torrentSessions[meta.InfoHash]; ok {
		return ts, nil
	}

	for idx, ts := range torrentSessions {
		err := ts.Quit()
		if err != nil {
			log.Println("Failed: ", err)
		}

		delete(torrentSessions, idx)
	}

	ts, err = NewTorrentSession(torrent, listenPort)
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return
	}
	log.Printf("Starting torrent session for %x", ts.m.InfoHash)
	go ts.DoTorrent()

	torrentSessions[ts.m.InfoHash] = ts

	lpd.Announce(ts.m.InfoHash)
	return
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
