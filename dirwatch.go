package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

var (
	errNewFile = errors.New("Got new file")
)

type Watcher struct {
	lastModTime time.Time
	torrentFile string
	watchedDir  string
	lock        sync.Mutex

	NewTorrent chan []byte
}

func NewWatcher(dir string) (w *Watcher) {
	u, err := user.Current()
	if err != nil {
		log.Fatal("Couldn't watch dir: ", err)
	}

	relpath := []string{".local", "share", "bitshare", "current.torrent"}
	torrentFile := u.HomeDir + string(os.PathSeparator) + filepath.Join(relpath...)

	lastModTime := time.Now()

	st, err := os.Stat(torrentFile)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Couldn't stat file: ", err)
	}
	if st != nil {
		lastModTime = st.ModTime()
	}

	w = &Watcher{
		lastModTime: lastModTime,
		torrentFile: torrentFile,
		watchedDir:  dir,
		NewTorrent:  make(chan []byte),
	}
	go w.watch()

	if w.lastModTime.Before(time.Now()) {
		content, err := ioutil.ReadFile(w.torrentFile)
		if err != nil {
			log.Fatal(err)
		}

		go func() {
			w.NewTorrent <- content
		}()
	}

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

			w.NewTorrent <- w.torrentify()
		} else {
			log.Println("Error while walking dir:", err)
		}
	}
}

func (w *Watcher) torrentify() (content []byte) {
	w.lock.Lock()
	defer w.lock.Unlock()

	cmd := exec.Command("transmission-create",
		"-o", w.torrentFile,
		w.watchedDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()

	st, err := os.Stat(w.torrentFile)
	if err != nil {
		log.Fatal(err)
	}

	w.lastModTime = st.ModTime()

	content, err = ioutil.ReadFile(w.torrentFile)
	if err != nil {
		log.Fatal(err)
	}

	return
}
