package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"io"
	"log"

	bencode "code.google.com/p/bencode-go"
)

type MetadataMessage struct {
	MsgType   uint8 "msg_type"
	Piece     uint  "piece"
	TotalSize uint  "total_size"
}

func (t *TorrentSession) DoMetadata(msg []byte, p *peerState) {
	// We need a buffered reader because the raw data is put directly
	// after the bencoded data, and a simple reader will get all its bytes
	// eaten. A buffered reader will keep a reference to where the
	// bdecoding ended.
	br := bufio.NewReader(bytes.NewReader(msg))
	var message MetadataMessage
	err := bencode.Unmarshal(br, &message)
	if err != nil {
		log.Println("Error when parsing metadata: ", err)
		return
	}

	mt := message.MsgType
	switch mt {
	case METADATA_REQUEST:
		//TODO: Answer to metadata request
	case METADATA_DATA:

		var piece bytes.Buffer
		_, err := io.Copy(&piece, br)
		if err != nil {
			log.Println("Error when getting metadata piece: ", err)
			return
		}
		t.si.ME.Pieces[message.Piece] = piece.Bytes()

		finished := true
		for idx, data := range t.si.ME.Pieces {
			if len(data) == 0 {
				p.sendMetadataRequest(idx)
				finished = false
			}
		}

		if !finished {
			break
		}

		log.Println("Finished downloading metadata!")
		var full bytes.Buffer
		for _, piece := range t.si.ME.Pieces {
			full.Write(piece)
		}
		b := full.Bytes()

		// Verify sha
		sha := sha1.New()
		sha.Write(b)
		actual := string(sha.Sum(nil))
		if actual != t.m.InfoHash {
			log.Println("Invalid metadata")
			log.Printf("Expected %s, got %s\n", t.m.InfoHash, actual)
		}

		metadata := string(b)
		err = saveMetaInfo(metadata)
		if err != nil {
			return
		}
		t.reload(metadata)
	case METADATA_REJECT:
		log.Printf("%d didn't want to send piece %d\n", p.address, message.Piece)
	default:
		log.Println("Didn't understand metadata extension type: ", mt)
	}
}
