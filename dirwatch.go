package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	errNewFile = errors.New("Got new file")
)

type Watcher struct {
	lastModTime time.Time
	bitshareDir string
	watchedDir  string
	lock        sync.Mutex

	PingNewTorrent chan string
}

func NewWatcher(bitshareDir, watchedDir string) (w *Watcher) {

	// Lastmodtime
	// If we have a current torrent, it's its mod time
	// Otherwise it's time.Now()
	defaultNow := time.Now()
	lastModTime := defaultNow

	currentFile := filepath.Join(bitshareDir, "current")
	st, err := os.Stat(currentFile)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Couldn't stat current file: ", err)
	}
	if st != nil {
		current, err := ioutil.ReadFile(currentFile)
		if err == nil {
			currentTorrent := filepath.Join(bitshareDir, string(current))
			st2, err := os.Stat(currentTorrent)
			if err == nil {
				lastModTime = st2.ModTime()
			}
		}
	}

	err = clean(bitshareDir)
	if err != nil {
		log.Fatal("Couldn't clean bitshare dir:", err)
	}

	w = &Watcher{
		lastModTime:    lastModTime,
		bitshareDir:    bitshareDir,
		watchedDir:     watchedDir,
		PingNewTorrent: make(chan string),
	}
	go w.watch()

	// Initialization
	var ih string

	if w.lastModTime.Before(defaultNow) {
		ih, err = currentTorrent(w.bitshareDir)
		if err != nil {
			log.Fatal("Couldn't get current infohash: ", err)
		}
	} else {
		ih = w.torrentify()
	}
	go func() {
		if ih != "" {
			w.PingNewTorrent <- ih
		}
	}()

	return
}

func currentTorrent(dir string) (ih string, err error) {
	currentFile := filepath.Join(dir, "current")
	st, err := os.Stat(currentFile)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Couldn't stat current file: ", err)
	}
	if st == nil {
		return
	}

	ihbytes, err := ioutil.ReadFile(currentFile)
	if err != nil {
		return
	}
	ih1, err := hex.DecodeString(string(ihbytes))
	ih = string(ih1)
	return
}

func (w *Watcher) watch() {
	for _ = range time.Tick(10 * time.Second) {
		w.lock.Lock()

		err := filepath.Walk(w.watchedDir, func(path string, info os.FileInfo, perr error) (err error) {
			if !info.Mode().IsRegular() {
				return
			}

			// Torrents can't have empty files
			if info.Size() == 0 {
				return
			}

			if info.ModTime().After(w.lastModTime) {
				fmt.Printf("[newer] %s\n", path)
				return errNewFile
			}

			return
		})

		w.lock.Unlock()

		if err == nil {
			continue
		}

		if err == errNewFile {
			// New torrent: block until we completely manage it. We will take
			// care of other changes in the next run of the loop.

			w.PingNewTorrent <- w.torrentify()
		} else {
			log.Println("Error while walking dir:", err)
		}
	}
}

func (w *Watcher) torrentify() (ih string) {
	w.lock.Lock()
	defer w.lock.Unlock()

	tmpFile, err := ioutil.TempFile(w.bitshareDir, "current.")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err = os.Remove(tmpFile.Name())
		if err != nil {
			log.Fatal(err)
		}
	}()

	err = tmpFile.Close()
	if err != nil {
		log.Fatal(err)
	}

	cmd := exec.Command("transmission-create",
		"-o", tmpFile.Name(),
		w.watchedDir)
	err = cmd.Run()
	if err != nil {
		log.Fatal(err)
	}

	// If resulting file is empty, we have an empty fileDir. Torrents
	// will come from CouchDB.
	st1, err := os.Stat(tmpFile.Name())
	if err != nil {
		log.Fatal(err)
	}
	if st1.Size() == 0 {
		return
	}

	// Get infohash
	m, err := NewMetaInfo(tmpFile.Name())
	if err != nil {
		log.Fatal(err)
	}
	ih = m.InfoHash
	ihhex := fmt.Sprintf("%x", ih)

	// Move tmp file to final file (with infohash as name)
	currentTorrent := filepath.Join(w.bitshareDir, ihhex)
	if st, err := os.Stat(currentTorrent); st != nil {
		if err = os.Remove(currentTorrent); err != nil {
			log.Fatal(err)
		}
	}
	err = os.Link(tmpFile.Name(), currentTorrent)
	if err != nil {
		log.Fatal(err)
	}

	// Overwrite "current" file with current value
	currentFile, err := os.Create(filepath.Join(w.bitshareDir, "current"))
	if err != nil {
		log.Fatal(err)
	}
	defer currentFile.Close()
	currentFile.WriteString(ihhex)

	// Update last mod time
	st, err := os.Stat(currentTorrent)
	if err != nil {
		log.Fatal(err)
	}
	w.lastModTime = st.ModTime()

	return
}

func clean(dirname string) (err error) {
	dir, err := os.Open(dirname)
	if err != nil {
		return
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return
	}
	for _, name := range names {
		if strings.HasPrefix(name, "current.") {
			err = os.Remove(filepath.Join(dirname, name))
			if err != nil {
				break
			}
		}
	}

	return
}
