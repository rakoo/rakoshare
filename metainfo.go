package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	bencode "code.google.com/p/bencode-go"
	"github.com/nictuku/dht"
)

type FileDict struct {
	Length int64
	Path   []string
	Md5sum string
}

type InfoDict struct {
	PieceLength int64 "piece length"
	Pieces      string
	Private     int64
	Name        string
	// Single File Mode
	Length int64
	Md5sum string
	// Multiple File mode
	Files []FileDict
}

type MetaInfo struct {
	Info         InfoDict
	InfoHash     string
	Announce     string
	AnnounceList [][]string "announce-list"
	CreationDate string     "creation date"
	Comment      string
	CreatedBy    string "created by"
	Encoding     string
}

func NewMetaInfo(torrent string) (m *MetaInfo, err error) {
	if strings.HasPrefix(torrent, "http:") {
		return NewMetaInfoFromHTTP(torrent)
	} else if strings.HasPrefix(torrent, "magnet:") {
		return NewMetaInfoFromMagnet(torrent)
	} else {
		return NewMetaInfoFromFile(torrent)
	}
}

func NewMetaInfoFromHTTP(torrent string) (m *MetaInfo, err error) {
	r, err := proxyHttpGet(torrent)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	content, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return
	}

	return NewMetaInfoFromContent(content)
}

func NewMetaInfoFromFile(torrent string) (m *MetaInfo, err error) {
	content, err := ioutil.ReadFile(torrent)
	if err != nil {
		return
	}

	return NewMetaInfoFromContent(content)
}

func NewMetaInfoFromContent(content []byte) (m *MetaInfo, err error) {

	var m1 MetaInfo
	err1 := bencode.Unmarshal(bytes.NewReader(content), &m1)
	if err1 != nil {
		err = errors.New("Couldn't parse torrent file: " + err.Error())
		return
	}

	hash := sha1.New()
	err1 = bencode.Marshal(hash, m1.Info)
	if err1 != nil {
		return
	}

	m1.InfoHash = string(hash.Sum(nil))

	return &m1, nil
}

func NewMetaInfoFromMagnet(torrent string) (m *MetaInfo, err error) {
	magnet, err := parseMagnet(torrent)
	if err != nil {
		log.Println("Couldn't parse magnet: ", err)
		return
	}

	ih, err := dht.DecodeInfoHash(magnet.InfoHashes[0])
	if err != nil {
		return
	}

	m = &MetaInfo{InfoHash: string(ih)}
	return

}

func getMetaInfo(torrent string) (metaInfo *MetaInfo, err error) {
	return NewMetaInfo(torrent)
}

type TrackerResponse struct {
	FailureReason  string "failure reason"
	WarningMessage string "warning message"
	Interval       time.Duration
	MinInterval    time.Duration "min interval"
	TrackerId      string        "tracker id"
	Complete       int
	Incomplete     int
	Peers          string
	Peers6         string
}

type SessionInfo struct {
	PeerId     string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64

	UseDHT      bool
	FromMagnet  bool
	HaveTorrent bool

	OurExtensions map[int]string
	ME            *MetaDataExchange
}

type MetaDataExchange struct {
	Transferring bool
	Pieces       [][]byte
}

func getTrackerInfo(url string) (tr *TrackerResponse, err error) {
	r, err := proxyHttpGet(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		data, _ := ioutil.ReadAll(r.Body)
		reason := "Bad Request " + string(data)
		log.Println(reason)
		err = errors.New(reason)
		return
	}
	var tr2 TrackerResponse
	err = bencode.Unmarshal(r.Body, &tr2)
	r.Body.Close()
	if err != nil {
		return
	}
	tr = &tr2
	return
}

func saveMetaInfo(metadata string) (err error) {
	var info InfoDict
	err = bencode.Unmarshal(bytes.NewReader([]byte(metadata)), &info)
	if err != nil {
		return
	}

	f, err := os.Create(info.Name + ".torrent")
	if err != nil {
		log.Println("Error when opening file for creation: ", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(metadata)

	return
}
