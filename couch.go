package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

const (
	COUCH_URL = `http://localhost:5984/bitshare`
)

type changesline struct {
	Doc *dbdoc `json:"doc"`
}

type PutResponse struct {
	Ok  string `json:"ok"`
	Id  string `json:"id"`
	Rev string `json:"rev"`
}

type dbdoc struct {
	Id     string `json:"_id"`
	Rev    string `json:"_rev,omitempty"`
	Magnet string `json:"magnet"`
}

type DB struct {
	newTorrent chan string

	lastRev    string
	lastMagnet string
}

func NewDB() (db *DB) {
	db = &DB{
		newTorrent: make(chan string),
	}
	err := db.Connect()
	if err != nil {
		log.Fatal("Couldn't connect to remote couchdb: ", err)
	}

	return db
}

func (db *DB) Connect() (err error) {
	existResp, err := http.Head(COUCH_URL)
	if err != nil {
		return
	}
	switch existResp.StatusCode {
	case 404:
		putReq, err := http.NewRequest("PUT", COUCH_URL, nil)
		if err != nil {
			return err
		}

		createResp, err := http.DefaultClient.Do(putReq)
		if err != nil {
			return err
		}
		if createResp.StatusCode != 201 {
			return errors.New(fmt.Sprintf("Couldn't create db: %s", createResp.Status))
		}
	case 200:
		break
	default:
		return errors.New(fmt.Sprintf("Couldn't connect to db: %s", existResp.Status))
	}

	docresp, err := http.Get(COUCH_URL + "/current")
	if err != nil {
		log.Println("Error when heading current version:", err)
		return
	}
	if docresp.StatusCode != 200 && docresp.StatusCode != 404 {
		log.Println("Error when heading current version:", docresp.Status)
		return
	}
	defer docresp.Body.Close()

	var jsdoc dbdoc
	err = json.NewDecoder(docresp.Body).Decode(&jsdoc)
	if err != nil {
		log.Println("Couldn't unmarshal current doc: ", err)
		return
	}
	db.lastRev = jsdoc.Rev
	db.lastMagnet = jsdoc.Magnet

	go func() {
		if db.lastMagnet != "" {
			db.newTorrent <- db.lastMagnet
		}
	}()

	go db.listenTorrentChanges()

	return
}

func (db *DB) PushNewTorrent(ih string) (err error) {
	doc := dbdoc{
		Id:     "current",
		Magnet: fmt.Sprintf("magnet:?xt=urn:btih:%x", ih),
		Rev:    db.lastRev,
	}

	if doc.Magnet == db.lastMagnet {
		return
	}

	var buf bytes.Buffer
	err = json.NewEncoder(&buf).Encode(doc)
	if err != nil {
		log.Println("Couldn't encode doc:", err)
		return
	}

	putReq, err := http.NewRequest("PUT", COUCH_URL+"/current", &buf)
	if err != nil {
		log.Println("Couldn't PUT to couchdb:", err)
		return
	}

	putResp, err := http.DefaultClient.Do(putReq)
	if putResp.StatusCode != 200 && putResp.StatusCode != 201 {
		log.Println("Error when putting doc:", putResp.Status)
	}
	defer putResp.Body.Close()

	var resp PutResponse
	err = json.NewDecoder(putResp.Body).Decode(&resp)
	if err != nil {
		return
	}

	db.lastRev = resp.Rev
	db.lastMagnet = doc.Magnet

	return
}

func (db *DB) listenTorrentChanges() (err error) {
	url := COUCH_URL + "/_changes?since=now&feed=continuous&include_docs=true"
	changesResp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer changesResp.Body.Close()

	rd := newLiner(changesResp.Body)
	for line := range rd.Lines() {
		var change changesline
		err := json.Unmarshal(line, &change)
		if err != nil {
			log.Println("Couldn't decode into document:", err)
			continue
		}

		if change.Doc != nil && change.Doc.Id != "" {
			db.newTorrent <- change.Doc.Magnet
		}
	}

	return
}

// A wrapper on bufio.Reader. Creates a channel on Lines that will spit
// reader line by line.
type liner struct {
	buf       bytes.Buffer
	bufreader *bufio.Reader
}

func newLiner(rd io.Reader) (l liner) {
	return liner{
		bufreader: bufio.NewReader(rd),
	}
}

func (l liner) Lines() (c chan []byte) {
	c = make(chan []byte)
	go l.split(c)

	return
}

func (l liner) split(c chan []byte) {
	for {
		line, isPrefix, err := l.bufreader.ReadLine()
		if err != nil {
			if err == io.EOF {
				continue
			}

			log.Fatal(err)
		}

		if len(line) == 0 {
			continue
		}

		_, err = l.buf.Write(line)
		if err != nil {
			log.Fatal(err)
		}

		if line != nil && !isPrefix {
			lastline := make([]byte, l.buf.Len())
			_, err := l.buf.Read(lastline)
			if err != nil {
				log.Fatal(err)
			}
			l.buf.Reset()

			c <- lastline
		}
	}
}
