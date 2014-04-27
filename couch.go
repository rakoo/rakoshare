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
	"net/textproto"
	"time"
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
	if len(ih) != 20 {
		return errors.New("Incorrect infohash")
	}

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

	var newRev string
	var newMagnet string

	feed := newCouchChangesFeed()
	for {
		select {
		case changeRaw := <-feed.Changes:
			var change changesline
			err := json.Unmarshal(changeRaw, &change)
			if err != nil {
				log.Println("Couldn't decode into document:", err)
				continue
			}

			if change.Doc != nil && change.Doc.Id != "" {
				newRev = change.Doc.Rev
				newMagnet = change.Doc.Magnet
			}
		case <-time.After(3 * time.Second):

			if newRev != "" && newRev != db.lastRev &&
				newMagnet != "" && newMagnet != db.lastMagnet {
				db.lastRev = newRev
				db.lastMagnet = newMagnet

				db.newTorrent <- db.lastMagnet
			}
		}
	}

	return
}

// a CouchChangesFeed listens to the _changes endpoint on CouchDB and produces
// all lines on its Lines channel
type CouchChangesFeed struct {
	Changes chan []byte
	url     string
}

func newCouchChangesFeed() CouchChangesFeed {
	f := CouchChangesFeed{
		url:     COUCH_URL + "/_changes?since=now&feed=continuous&include_docs=true",
		Changes: make(chan []byte),
	}
	go f.SendLines()
	return f
}

func (f CouchChangesFeed) SendLines() {
	for {
		changesResp, err := http.Get(f.url)
		if err != nil {
			log.Fatal(err)
		}

		linesReader := textproto.NewReader(bufio.NewReader(changesResp.Body))
		for {
			line, err := linesReader.ReadLineBytes()
			if err != nil {
				if err == io.EOF {
					break
				}

				log.Println(err)
				continue
			}
			f.Changes <- line
		}

		changesResp.Body.Close()
	}

	return
}
