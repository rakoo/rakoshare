package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
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

type Watcher struct {
	lastModTime time.Time
	workDir     string
	watchedDir  string
	lock        sync.Mutex

	PingNewTorrent chan string
}

func NewWatcher(workDir, watchedDir string) (w *Watcher) {

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
	go w.watch()

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
	} else if os.IsNotExist(err) {
		return
	}

	var mess IHMessage
	err = bencode.NewDecoder(current).Decode(&mess)
	if err == nil {
		ih1, err := hex.DecodeString(mess.Info.InfoHash)
		if err == nil {
			return string(ih1), nil
		}
	} else if err != io.EOF {
		log.Printf("Error when decoding \"current\": %s\n", err)
		return
	}

	// No torrent but there is content. Calculate manually.
	return w.torrentify(), nil
}

func (w *Watcher) watch() {
	for _ = range time.Tick(10 * time.Second) {
		w.lock.Lock()

		err := torrentWalk(w.watchedDir, func(path string, info os.FileInfo, perr error) (err error) {
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

	tmpFile, err := ioutil.TempFile(w.workDir, "current.")
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

	meta, err := createMeta(w.watchedDir)
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create(tmpFile.Name())
	if err != nil {
		log.Fatal("Couldn't create out file: ", err)
	}
	err = bencode.NewEncoder(f).Encode(meta)
	if err != nil {
		log.Fatal("Couldn't create torrent: ", err)
	}
	f.Close()

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
	currentTorrent := filepath.Join(w.workDir, ihhex)
	if st, err := os.Stat(currentTorrent); st != nil {
		if err = os.Remove(currentTorrent); err != nil {
			log.Fatal(err)
		}
	}
	err = os.Link(tmpFile.Name(), currentTorrent)
	if err != nil {
		log.Fatal(err)
	}

	// Update last mod time
	st, err := os.Stat(currentTorrent)
	if err != nil {
		log.Fatal(err)
	}
	w.lastModTime = st.ModTime()

	return
}

func createMeta(dir string) (meta *MetaInfo, err error) {
	blockSize := int64(1 << 20) // 1MiB

	fileDicts := make([]*FileDict, 0)

	hasher := NewBlockHasher(blockSize)
	err = torrentWalk(dir, func(path string, info os.FileInfo, perr error) (err error) {
		f, err := os.Open(path)
		if err != nil {
			log.Fatalf("Couldn't open %s for hashing: %s\n", path, err)
		}
		defer f.Close()

		_, err = io.Copy(hasher, f)
		if err != nil {
			log.Fatalf("Couldn't hash %s: %s\n", path, err)
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
		log.Fatal("Couldn't hash files correctly: ", err)
	}

	meta = &MetaInfo{
		Info: &InfoDict{
			Pieces:      string(hasher.Pieces),
			PieceLength: blockSize,
			Private:     0,
			Name:        "tmp",
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
					log.Fatal("Error when sha1-ing: ", err)
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
	if h.left > 0 {
		h.Pieces = h.sha1er.Sum(h.Pieces)
	}
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
