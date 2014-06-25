package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"io"
	"log"

	bencode "code.google.com/p/bencode-go"
)

const (
	METADATA_REQUEST = iota
	METADATA_DATA
	METADATA_REJECT
)

const (
	METADATA_PIECE_SIZE = 1 << 14 // 16kiB
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
		if !t.si.HaveTorrent {
			break
		}

		rawInfo := t.m.RawInfo()

		from := int(message.Piece * METADATA_PIECE_SIZE)

		// Piece asked must be between the first one and the last one.
		// Note that the last one will most of the time be smaller than
		// METADATA_PIECE_SIZE
		if from >= len(rawInfo) {
			log.Printf("%d is out of range. Not sending this\n", message.Piece)
			break
		}

		to := int(from + METADATA_PIECE_SIZE)
		if to > len(rawInfo) {
			to = len(rawInfo)
		}

		if _, ok := p.theirExtensions["ut_metadata"]; !ok {
			log.Println("%s doesn't understand ut_metadata\n", p.address)
			break
		}

		respHeader := MetadataMessage{
			MsgType: METADATA_DATA,
			Piece:   message.Piece,
		}

		var resp bytes.Buffer
		resp.WriteByte(EXTENSION)
		resp.WriteByte(byte(p.theirExtensions["ut_metadata"]))

		err = bencode.Marshal(&resp, respHeader)
		if err != nil {
			log.Println("Couldn't header metadata response: ", err)
			break
		}

		resp.Write(rawInfo[from:to])
		p.sendMessage(resp.Bytes())

	case METADATA_DATA:

		if t.si.HaveTorrent {
			break
		}

		var piece bytes.Buffer
		_, err := io.Copy(&piece, br)
		if err != nil {
			log.Println("Error when getting metadata piece: ", err)
			return
		}

		if int(message.Piece) >= len(t.si.ME.Pieces) {
			log.Printf("Rejecting invalid metadata piece %d, max is %d\n",
				message.Piece, len(t.si.ME.Pieces)-1)
			break
		}

		t.si.ME.Pieces[message.Piece] = piece.Bytes()

		finished := true
		for idx, data := range t.si.ME.Pieces {
			if len(data) == 0 {
				p.sendMetadataRequest(idx)
				finished = false
				break
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
		info := full.Bytes()

		// Verify sha
		sha := sha1.New()
		sha.Write(info)
		actual := string(sha.Sum(nil))
		if actual != t.m.InfoHash {
			log.Println("Invalid metadata")
			log.Printf("Expected %x, got %x\n", t.m.InfoHash, actual)
			break
		}

		err = t.reload(info)
		if err != nil {
			return
		}

		if p.have == nil {
			if p.temporaryBitfield != nil {
				p.have = NewBitsetFromBytes(t.totalPieces, p.temporaryBitfield)
				p.temporaryBitfield = nil
			} else {
				p.have = NewBitset(t.totalPieces)
			}
		}
		if p.have == nil {
			log.Panic("Invalid bitfield data")
		}

		p.SendBitfield(t.pieceSet)
	case METADATA_REJECT:
		log.Printf("%d didn't want to send piece %d\n", p.address, message.Piece)
	default:
		log.Println("Didn't understand metadata extension type: ", mt)
	}
}
