package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/bencode"
)

var (
	errNewFile = errors.New("Got new file")
)

type state int

const (
	IDEM = iota
	CHANGED
)

type Watcher struct {
	lastModTime time.Time
	workDir     string
	watchedDir  string
	lock        sync.Mutex

	PingNewTorrent chan string
}

func NewWatcher(workDir, watchedDir string, canWrite bool) (w *Watcher) {

	if _, err := os.Stat(workDir); err != nil {
		if os.IsNotExist(err) {
			os.MkdirAll(workDir, os.ModeDir|0755)
		}
	}

	// Lastmodtime
	// If we have a current torrent, it's its mod time
	// Otherwise it's time.Now()
	defaultNow := time.Now()
	lastModTime := defaultNow

	currentFile := filepath.Join(workDir, "current")
	st, err := os.Stat(currentFile)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Couldn't stat current file: ", err)
	}
	if st != nil {
		lastModTime = st.ModTime()
	}

	err = clean(workDir)
	if err != nil {
		log.Fatal("Couldn't clean workDir dir:", err)
	}

	w = &Watcher{
		lastModTime:    lastModTime,
		workDir:        workDir,
		watchedDir:     watchedDir,
		PingNewTorrent: make(chan string),
	}

	if canWrite {
		go w.watch()
	}

	// Initialization, only if there is something in the dir
	if _, err := os.Stat(watchedDir); err != nil {
		return
	}

	dir, err := os.Open(watchedDir)
	if err != nil {
		return
	}

	names, err := dir.Readdirnames(1)
	if len(names) == 0 || err != nil && err != io.EOF {
		return
	}

	ih, err := w.currentTorrent()
	if err == nil {
		go func() {
			w.PingNewTorrent <- ih
		}()
	}

	return
}

func (w *Watcher) currentTorrent() (ih string, err error) {
	currentFile := filepath.Join(w.workDir, "current")
	current, err := os.Open(currentFile)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Couldn't stat current file: ", err)
	} else if err == nil {
		var mess IHMessage
		err = bencode.NewDecoder(current).Decode(&mess)
		if err == nil {
			return mess.Info.InfoHash, nil
		} else if err != io.EOF {
			log.Printf("Error when decoding \"current\": %s\n", err)
			return
		}
	}

	// No torrent but there is content. Calculate manually.
	return w.torrentify()
}

func (w *Watcher) watch() {
	var previousState, currentState state
	currentState = IDEM

	compareTime := w.lastModTime

	for _ = range time.Tick(10 * time.Second) {
		w.lock.Lock()

		err := torrentWalk(w.watchedDir, func(path string, info os.FileInfo, perr error) (err error) {
			if info.ModTime().After(compareTime) {
				fmt.Printf("[newer] %s\n", path)
				return errNewFile
			}
			return
		})

		w.lock.Unlock()

		previousState = currentState

		if err == errNewFile {
			currentState = CHANGED
		} else if err == nil {
			currentState = IDEM
		} else {
			log.Println("Error while walking dir:", err)
		}

		compareTime = time.Now()

		if currentState == IDEM && previousState == CHANGED {
			// Note that we may be in the CHANGED state for multiple
			// iterations, such as when changes take more than 10 seconds to
			// finish. When we go back to "idle" state, we kick in the
			// metadata creation.

			// Block until we completely manage it. We will take
			// care of other changes in the next run of the loop.
			ih, err := w.torrentify()
			if err != nil {
				log.Printf("Couldn't torrentify: ", err)
				continue
			}
			w.PingNewTorrent <- ih
		}
	}
}

func (w *Watcher) torrentify() (ih string, err error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	meta, err := createMeta(w.watchedDir)
	if err != nil {
		log.Println(err)
		return
	}

	err = w.saveMetainfo(meta)

	return meta.InfoHash, err
}

func (w *Watcher) saveMetainfo(meta *MetaInfo) error {

	tmpFile, err := ioutil.TempFile(w.workDir, "current.")
	if err != nil {
		return err
	}
	defer func() {
		err = os.Remove(tmpFile.Name())
		if err != nil {
			return
		}
	}()

	err = tmpFile.Close()
	if err != nil {
		return err
	}
	ihhex := fmt.Sprintf("%x", meta.InfoHash)

	f, err := os.Create(tmpFile.Name())
	if err != nil {
		return err
	}
	err = bencode.NewEncoder(f).Encode(meta)
	if err != nil {
		return err
	}
	f.Close()

	// Move tmp file to final file (with infohash as name)
	currentTorrent := filepath.Join(w.workDir, ihhex)
	if st, err := os.Stat(currentTorrent); st != nil {
		if err = os.Remove(currentTorrent); err != nil {
			return err
		}
	}
	err = os.Link(tmpFile.Name(), currentTorrent)
	if err != nil {
		return err
	}

	// Update last mod time
	st, err := os.Stat(currentTorrent)
	if err != nil {
		return err
	}
	w.lastModTime = st.ModTime()

	return nil
}

func createMeta(dir string) (meta *MetaInfo, err error) {
	blockSize := int64(1 << 20) // 1MiB

	fileDicts := make([]*FileDict, 0)

	hasher := NewBlockHasher(blockSize)
	err = torrentWalk(dir, func(path string, info os.FileInfo, perr error) (err error) {
		if perr != nil {
			return perr
		}

		f, err := os.Open(path)
		if err != nil {
			return errors.New(fmt.Sprintf("Couldn't open %s for hashing: %s\n", path, err))
		}
		defer f.Close()

		_, err = io.Copy(hasher, f)
		if err != nil {
			log.Printf("Couldn't hash %s: %s\n", path, err)
			return err
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return
		}

		fileDict := &FileDict{
			Length: info.Size(),
			Path:   strings.Split(relPath, string(os.PathSeparator)),
		}
		fileDicts = append(fileDicts, fileDict)

		return
	})
	if err != nil {
		return
	}

	end := hasher.Close()
	if end != nil {
		return
	}

	meta = &MetaInfo{
		Info: &InfoDict{
			Pieces:      string(hasher.Pieces),
			PieceLength: blockSize,
			Private:     0,
			Name:        "rakoshare",
			Files:       fileDicts,
		},
	}

	hash := sha1.New()
	err = bencode.NewEncoder(hash).Encode(meta.Info)
	if err != nil {
		return
	}
	meta.InfoHash = string(hash.Sum(nil))

	return
}

type BlockHasher struct {
	sha1er    hash.Hash
	left      int64
	blockSize int64
	Pieces    []byte
}

func NewBlockHasher(blockSize int64) (h *BlockHasher) {
	return &BlockHasher{
		blockSize: blockSize,
		sha1er:    sha1.New(),
		left:      blockSize,
	}
}

// You shouldn't use this one
func (h *BlockHasher) Write(p []byte) (n int, err error) {
	n2, err := h.ReadFrom(bytes.NewReader(p))
	return int(n2), err
}

func (h *BlockHasher) ReadFrom(rd io.Reader) (n int64, err error) {
	var stop bool

	for {
		if h.left > 0 {
			thisN, err := io.CopyN(h.sha1er, rd, h.left)
			if err != nil {
				if err == io.EOF {
					stop = true
				} else {
					return n, err
				}
			}
			h.left -= thisN
			n += thisN
		}
		if h.left == 0 {
			h.Pieces = h.sha1er.Sum(h.Pieces)
			h.sha1er = sha1.New()
			h.left = h.blockSize
		}

		if stop {
			break
		}
	}

	return
}

func (h *BlockHasher) Close() (err error) {
	if h.left == h.blockSize {
		// We're at the end of a blockSize, we don't have any buffered data
		return
	}
	h.Pieces = h.sha1er.Sum(h.Pieces)
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

func torrentWalk(root string, fn filepath.WalkFunc) (err error) {
	return filepath.Walk(root, func(path string, info os.FileInfo, perr error) (err error) {
		if !info.Mode().IsRegular() {
			return
		}

		// Torrents can't have empty files
		if info.Size() == 0 {
			return
		}

		if strings.HasPrefix(filepath.Base(path), ".") {
			return
		}

		if filepath.Ext(path) == ".part" {
			return
		}

		return fn(path, info, perr)
	})
}
