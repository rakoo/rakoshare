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

type dbdoc struct {
	Id     string `json:"id"`
	Magnet string `json:"magnet"`
}

type DB struct {
	newTorrent chan struct{}
}

func NewDB() (db DB) {
	db = DB{
		newTorrent: make(chan struct{}),
	}
	err := db.Connect()
	if err != nil {
		log.Fatal("Couldn't connect to remote couchdb: ", err)
	}

	return db
}

func (db DB) Connect() (err error) {
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

	go db.listenTorrentChanges()

	return
}

func (db DB) putNewTorrent(ih string) (err error) {
	return
}

func (db DB) listenTorrentChanges() (err error) {
	url := COUCH_URL + "/_changes?since=now&feed=continuous&include_docs=true"
	changesResp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer changesResp.Body.Close()

	rd := newLiner(changesResp.Body)
	for line := range rd.Lines() {
		var doc dbdoc
		err := json.Unmarshal(line, &doc)
		if err != nil {
			log.Println("Couldn't decode into document:", err)
			continue
		}

		if doc.Id != "" {
			db.newTorrent <- struct{}{}
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
