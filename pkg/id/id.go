package id

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"io"

	"code.google.com/p/go.crypto/scrypt"

	ed "github.com/agl/ed25519"
	"github.com/crowsonkb/base58"
)

type Role byte

var (
	ROLE_WRITEREADSTORE Role = 0x1
	ROLE_READSTORE      Role = 0x2
	ROLE_STORE          Role = 0x4
)

var (
	errInvalidId       = errors.New("Invalid id")
	errInvalidInfoHash = errors.New("Programming error: Invalid infohash generated")
)

type PubKey [ed.PublicKeySize]byte
type PrivKey [ed.PrivateKeySize]byte
type PreSharedKey [32]byte

// len = 20
type infohash []byte

func deriveFromSeed(seed [32]byte) (priv PrivKey, pub PubKey, psk PreSharedKey, ih infohash, err error) {
	// priv/pub key
	tmpPub, tmpPriv, err := ed.GenerateKey(bytes.NewReader(seed[:32]))
	if err != nil {
		return
	}
	priv = *tmpPriv
	pub = *tmpPub
	psk, err = deriveScrypt(pub)

	if err != nil {
		return
	}

	outscrypt, err := deriveScrypt(psk)
	if err != nil {
		return
	}
	ih = outscrypt[:sha1.Size]

	return
}

func deriveScrypt(in [32]byte) (psk [32]byte, err error) {
	out, err := scrypt.Key(in[:], in[:], 1<<16, 8, 1, 32)
	if err != nil {
		return
	}
	copy(psk[:], out)

	return
}

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
// Finally, when derived a third time, we get a infohash like the
// Bittorrent ones, that is used to find peers.
type Id struct {
	Priv     PrivKey
	canWrite bool

	Pub     PubKey
	canRead bool

	Psk PreSharedKey

	// This is automatically generated from Psk. It must be 20 bytes long
	Infohash []byte
}

func New() (Id, error) {

	var randSeed [32]byte
	_, err := io.ReadFull(rand.Reader, randSeed[:])
	if err != nil {
		return Id{}, err
	}

	priv, pub, psk, ih, err := deriveFromSeed(randSeed)

	id := Id{
		Priv:     priv,
		canWrite: true,

		Pub:     pub,
		canRead: true,

		Psk: psk,

		Infohash: ih,
	}

	return id, err
}

func (id Id) CanWrite() bool {
	return id.canWrite
}

func (id Id) CanRead() bool {
	return id.canRead
}

func (id Id) WRS() string {
	if !id.CanWrite() {
		return ""
	}
	wrs := make([]byte, len(id.Priv)+1)
	wrs[0] = byte(ROLE_WRITEREADSTORE)
	copy(wrs[1:], id.Priv[:])
	return base58.Encode(wrs)
}

func (id Id) RS() string {
	if !id.CanRead() {
		return ""
	}
	rs := make([]byte, len(id.Pub)+1)
	rs[0] = byte(ROLE_READSTORE)
	copy(rs[1:], id.Pub[:])
	return base58.Encode(rs)
}

func (id Id) S() string {
	s := make([]byte, len(id.Psk)+1)
	s[0] = byte(ROLE_STORE)
	copy(s[1:], id.Psk[:])
	return base58.Encode(s)
}

func NewFromString(in string) (id Id, err error) {
	if len(in) <= 1 {
		err = errInvalidId
		return
	}
	decoded, err := base58.Decode(in)
	if err != nil {
		return
	}

	role := Role(decoded[0])
	switch role {
	case ROLE_WRITEREADSTORE:
		if len(decoded[1:]) != ed.PrivateKeySize {
			err = errInvalidId
			break
		}

		priv := decoded[1:]
		var privId PrivKey
		copy(privId[:], priv)

		pub := priv[32:]
		var pubId PubKey
		copy(pubId[:], pub)

		psk, err := deriveScrypt(pubId)
		if err != nil {
			break
		}

		outscrypt, err := deriveScrypt(psk)
		if err != nil {
			break
		}

		id = Id{
			Priv:     privId,
			canWrite: true,

			Pub:     pubId,
			canRead: true,

			Psk: psk,

			Infohash: outscrypt[:sha1.Size],
		}

	case ROLE_READSTORE:
		if len(decoded[1:]) != ed.PublicKeySize {
			err = errInvalidId
			break
		}

		pub := decoded[1:]
		var pubId PubKey
		copy(pubId[:], pub)

		psk, err := deriveScrypt(pubId)
		if err != nil {
			break
		}

		outscrypt, err := deriveScrypt(psk)
		if err != nil {
			break
		}

		id = Id{
			Pub:     pubId,
			canRead: true,

			Psk: psk,

			Infohash: outscrypt[:sha1.Size],
		}
	case ROLE_STORE:
		if len(decoded[1:]) != 32 {
			err = errInvalidId
			break
		}

		var psk PreSharedKey
		copy(psk[:], decoded[1:])

		outscrypt, err := deriveScrypt(psk)
		if err != nil {
			break
		}

		id = Id{
			Psk: psk,

			Infohash: outscrypt[:sha1.Size],
		}
	default:
		err = errInvalidId
	}

	return
}
