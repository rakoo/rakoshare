package id

import (
	"errors"
	"fmt"
	"io"
	"log"

	"bytes"

	"crypto/rand"
	"crypto/sha1"

	"code.google.com/p/go.crypto/scrypt"
	"code.google.com/p/go.crypto/sha3"
	ed "github.com/agl/ed25519"
	"github.com/crowsonkb/base58"
)

type CommonFormat [32]byte

type Role byte

var (
	WriteReadStore Role = 0x1
	ReadStore      Role = 0x2
	Store          Role = 0x4
)

type PubKey [ed.PublicKeySize]byte
type PrivKey [ed.PrivateKeySize]byte
type EncryptionKey [32]byte
type PreSharedKey [32]byte

// The ID structure represents all the information a peer needs to know
// to be able to participate in a swarm sharing a given directory.
//
// IDs start from the highest privileged type: a peer handling such an
// ID has the ability to write content to the folder, along with reading
// and storing its content. Its human-intelligible value can be
// retrieved from WriteReadStoreID
//
// When derived once, the new ID now has lost the ability to write, but
// can still read and store content. Its human-intelligible value can be
// retrieved from ReadStoreID.
//
// When derived a second time, the new ID can only store content; it
// cannot read it (because the actual content is encrypted). The
// corresponding value is StoreID.
//
// IDs also contain all information needed to find the corresponding
// swarm and connect to peers.
type Id struct {
	Priv PrivKey
	Pub  PubKey

	// The encryption key, available for ReadStore and above
	Enc EncryptionKey

	// The preshared key, available for Store and above
	Psk PreSharedKey

	// The base58 encoded ids to be shared with other humans
	// Some of them may not be filled, depending on the capabilities
	// associated with this id
	WriteReadStoreID string
	ReadStoreID      string
	StoreID          string

	TorrentInfoHash [sha1.Size]byte
}

func (id *Id) CanWrite() bool {
	return id.WriteReadStoreID != ""
}
func (id *Id) CanRead() bool {
	return id.ReadStoreID != ""
}

func New() (*Id, error) {
	id := new(Id)

	var randSeed [32]byte
	_, err := io.ReadFull(rand.Reader, randSeed[:])
	if err != nil {
		return nil, err
	}

	err = id.fillWRS(randSeed)
	return id, err
}

func (id *Id) fillWRS(randSeed [32]byte) error {
	id.WriteReadStoreID = encodeWriteReadStore(randSeed)

	// priv/pub key
	var err error
	var tmpPub *[32]byte
	var tmpPriv *[64]byte
	tmpPub, tmpPriv, err = ed.GenerateKey(bytes.NewReader(randSeed[:]))
	if err != nil {
		return err
	}
	id.Priv = PrivKey(*tmpPriv)

	var sha3Priv [32]byte
	sha := sha3.NewKeccak512()
	sha.Write(id.Priv[:])
	copy(sha3Priv[:], sha.Sum(nil))
	enc := derive(sha3Priv)

	return id.fillRS(PubKey(*tmpPub), EncryptionKey(enc))
}

func (id *Id) fillRS(pub PubKey, enc EncryptionKey) error {
	id.ReadStoreID = encodeWithRole(CommonFormat(enc), pub, ReadStore)
	id.Enc = enc
	psk := derive(CommonFormat(enc))
	return id.fillS(pub, PreSharedKey(psk))
}

func (id *Id) fillS(pub PubKey, psk PreSharedKey) error {
	id.StoreID = encodeWithRole(CommonFormat(psk), pub, Store)
	id.Psk = psk
	id.Pub = pub

	// Torrent id
	forSha1 := derive(CommonFormat(id.Psk))
	id.TorrentInfoHash = sha1.Sum(forSha1[:])

	return nil
}

func NewFromString(in string) (*Id, error) {
	if len(in) <= 1 {
		return nil, errors.New("Invalid id string")
	}

	decoded, err := base58.Decode(in)
	if err != nil {
		return nil, err
	}

	id := new(Id)
	switch Role(decoded[0]) {
	case WriteReadStore:
		if len(decoded) != 1+32 {
			break
		}

		var seed [32]byte
		copy(seed[:], decoded[1:])
		id.fillWRS(seed)
		return id, nil

	case ReadStore:
		if len(decoded) != 1+32+32 {
			break
		}

		var pub PubKey
		copy(pub[:], decoded[1:len(pub)+1])

		var enc EncryptionKey
		copy(enc[:], decoded[1+len(pub):])
		id.fillRS(pub, enc)
		return id, nil

	case Store:
		if len(decoded) != 1+32+32 {
			break
		}

		var pub PubKey
		copy(pub[:], decoded[1:len(pub)+1])

		var psk PreSharedKey
		copy(psk[:], decoded[1+len(pub)+1:])
		id.fillS(pub, psk)
		return id, nil

	default:
		return nil, errors.New(fmt.Sprintf("Invalid role: %d", decoded[0]))
	}

	return nil, errors.New(fmt.Sprintf("Invalid len: %d", len(decoded)))
}

func derive(in CommonFormat) CommonFormat {
	sha3er := sha3.NewKeccak512()
	sha3er.Write(in[:])
	salt := sha3er.Sum(nil)[:8]

	out, err := scrypt.Key(in[:], salt, 1<<16, 8, 1, 32)
	if err != nil {
		log.Fatal(err)
	}

	var outBlob CommonFormat
	copy(outBlob[:], out)
	return outBlob
}

func encodeWriteReadStore(rand [32]byte) string {
	cp := make([]byte, len(rand)+1)
	cp[0] = byte(WriteReadStore)
	copy(cp[1:], rand[:])
	return base58.Encode(cp)
}

func encodeWithRole(blob CommonFormat, pubkey PubKey, r Role) string {
	cp := make([]byte, len(blob)+ed.PublicKeySize+1)
	cp[0] = byte(r)
	copy(cp[1:ed.PublicKeySize+1], pubkey[:])
	copy(cp[1+ed.PublicKeySize:], blob[:])
	return base58.Encode(cp)
}
