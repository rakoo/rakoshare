package main

import (
	"errors"
	"io"
	"log"
	"os"
	"path"
	"strings"
)

type FileStore interface {
	io.ReaderAt
	io.WriterAt
	io.Closer

	// Set all pieces from this one to be bad
	SetBad(from int64)

	// When downloading is finished, call Finish to move .part files to
	// real files
	Cleanup() error
}

type fileEntry struct {
	length int64
	name   string
}

type fileStore struct {
	offsets []int64
	files   []fileEntry // Stored in increasing globalOffset order
}

func (fe *fileEntry) open(name string, length int64) (err error) {
	partname := name + ".part"
	_, parterr := os.Stat(partname)
	if parterr == nil {
		parterr = os.Remove(partname)
		if parterr != nil {
			log.Printf("Couldn't remove part file: ", parterr)
		}
	}

	fe.length = length
	fe.name = name
	st, err := os.Stat(name)

	if err != nil && os.IsNotExist(err) {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
	} else {
		if st.Size() == length {
			return
		}
	}
	err = os.Truncate(name, length)
	if err != nil {
		return
		err = errors.New("Could not truncate file.")
	}
	return
}

func (fe *fileEntry) isPart() bool {
	return strings.HasSuffix(fe.name, ".part")
}

func (fe *fileEntry) SetPart() {
	if fe.isPart() {
		return
	}

	err := copyfile(fe.name, fe.name+".part")
	if err != nil {
		log.Println("Error at copying to .part file: ", err)
	}

	fe.name = fe.name + ".part"
}

func (fe *fileEntry) ReadAt(p []byte, off int64) (n int, err error) {
	file, err := os.OpenFile(fe.name, os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	return file.ReadAt(p, off)
}

func (fe *fileEntry) WriteAt(p []byte, off int64) (n int, err error) {
	file, err := os.OpenFile(fe.name, os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	return file.WriteAt(p, off)
}

func (fe *fileEntry) Cleanup() (err error) {
	if fe.isPart() {
		realname := strings.Replace(fe.name, ".part", "", 1)
		err = copyfile(fe.name, realname)
		if err != nil {
			log.Printf("Couldn't copy to real file: ", err)
		}

		err = os.Remove(fe.name)
		if err != nil {
			log.Printf("Couldn't remove part file: ", err)
		}
		fe.name = realname
	}

	return
}

func ensureDirectory(fullPath string) (err error) {
	fullPath = path.Clean(fullPath)
	if !strings.HasPrefix(fullPath, "/") {
		// Transform into absolute path.
		var cwd string
		if cwd, err = os.Getwd(); err != nil {
			return
		}
		fullPath = cwd + "/" + fullPath
	}
	base, _ := path.Split(fullPath)
	if base == "" {
		panic("Programming error: could not find base directory for absolute path " + fullPath)
	}
	err = os.MkdirAll(base, 0755)
	return
}

func NewFileStore(info *InfoDict, storePath string) (f FileStore, totalSize int64, err error) {
	fs := new(fileStore)
	numFiles := len(info.Files)
	if numFiles == 0 {
		// Create dummy Files structure.
		info = &InfoDict{Files: []*FileDict{&FileDict{info.Length, []string{info.Name}, info.Md5sum}}}
		numFiles = 1
	}
	fs.files = make([]fileEntry, numFiles)
	fs.offsets = make([]int64, numFiles)
	for i, _ := range info.Files {
		src := info.Files[i]
		// Clean the source path before appending to the storePath. This
		// ensures that source paths that start with ".." can't escape.
		cleanSrcPath := path.Clean("/" + path.Join(src.Path...))[1:]
		fullPath := path.Join(storePath, cleanSrcPath)
		err = ensureDirectory(fullPath)
		if err != nil {
			return
		}
		err = fs.files[i].open(fullPath, src.Length)
		if err != nil {
			return
		}
		fs.offsets[i] = totalSize
		totalSize += src.Length
	}
	f = fs
	return
}

func (f *fileStore) find(offset int64) int {
	// Binary search
	offsets := f.offsets
	low := 0
	high := len(offsets)
	for low < high-1 {
		probe := (low + high) / 2
		entry := offsets[probe]
		if offset < entry {
			high = probe
		} else {
			low = probe
		}
	}
	return low
}

func (f *fileStore) ReadAt(p []byte, off int64) (n int, err error) {
	index := f.find(off)
	for len(p) > 0 && index < len(f.offsets) {
		chunk := int64(len(p))
		entry := &f.files[index]
		itemOffset := off - f.offsets[index]
		if itemOffset < entry.length {
			space := entry.length - itemOffset
			if space < chunk {
				chunk = space
			}
			var nThisTime int
			nThisTime, err = entry.ReadAt(p[0:chunk], itemOffset)
			n = n + nThisTime
			if err != nil {
				return
			}
			p = p[nThisTime:]
			off += int64(nThisTime)
		}
		index++
	}
	// At this point if there's anything left to read it means we've run off the
	// end of the file store. Read zeros. This is defined by the bittorrent protocol.
	for i, _ := range p {
		p[i] = 0
	}
	return
}

func (f *fileStore) WriteAt(p []byte, off int64) (n int, err error) {
	index := f.find(off)
	for len(p) > 0 && index < len(f.offsets) {
		chunk := int64(len(p))
		entry := &f.files[index]
		itemOffset := off - f.offsets[index]
		if itemOffset < entry.length {
			space := entry.length - itemOffset
			if space < chunk {
				chunk = space
			}
			var nThisTime int
			nThisTime, err = entry.WriteAt(p[0:chunk], itemOffset)
			n += nThisTime
			if err != nil {
				return
			}
			p = p[nThisTime:]
			off += int64(nThisTime)
		}
		index++
	}
	// At this point if there's anything left to write it means we've run off the
	// end of the file store. Check that the data is zeros.
	// This is defined by the bittorrent protocol.
	for i, _ := range p {
		if p[i] != 0 {
			err = errors.New("Unexpected non-zero data at end of store.")
			n = n + i
			return
		}
	}
	n = n + len(p)
	return
}

func (f *fileStore) SetBad(from int64) {
	index := f.find(from)
	for index < len(f.offsets) {
		entry := &f.files[index]
		entry.SetPart()
		index++
	}
}

func (f *fileStore) Cleanup() (err error) {
	for _, fe := range f.files {
		err = fe.Cleanup()
	}

	return
}

func (f *fileStore) Close() (err error) {
	return
}

func copyfile(fromname, toname string) (err error) {
	from, err := os.Open(fromname)
	if err != nil {
		log.Printf("Couldn't open %s for read: %s", fromname, err)
		return err
	}
	defer from.Close()

	to, err := os.Create(toname)
	if err != nil {
		log.Printf("Couldn't open %s for write: %s", toname, err)
		return err
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		log.Printf("Couldn't copy to part: %s", err)
		return err
	}

	return
}
