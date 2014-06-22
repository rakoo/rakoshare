package id

import (
	"bytes"
	"fmt"
	"testing"
)

type vec struct {
	WRS string
	RS  string

	Pub string
	Enc string
	TIH string
}

var (
	vector1 = vec{
		WRS: "WjtGTvF26veb1YE1pRMBMKVnkD17p4wafwtDeqbf73Rw",
		RS:  "EpudJqATKA1TaH7fgsP31XveF5UmC2eGHrjGgmXmDF9Z1h3rR8Y52rntw6hCvcAV9uWkHk3SNFPYmHuQV98Eh6cN",
		Pub: "b388d202bb7e6eca82b38c321495f686320e9790490d356769e67fc660a31c1c",
		Enc: "a214a6c73d48bdd2f960a5f3f58ce4a341ac5b3b3a9d429e3689b8dda8a48827",
		TIH: "268214e30da706ff496f9fabd00193f57bc4bdfd",
	}
)

func TestNew(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatal("There should be no error for creating an id!")
	}
	if id == nil {
		t.Fatal("Id should not be nil!")
	}

	if !id.CanWrite() {
		t.Fatal("should be able to write")
	}

	if !id.CanRead() {
		t.Fatal("should be able to read")
	}
}

func TestNewFromString(t *testing.T) {
	idGarbage, err := NewFromString("GARBAGE")
	if err == nil || idGarbage != nil {
		t.Fatal("Garbage shouldn't be accepted!")
	}

	id, err := NewFromString(vector1.WRS)
	if err != nil {
		t.Fatal(err)
	}
	if id == nil {
		t.Fatal("Couldn't decode string!")
	}
	if !id.CanWrite() || !id.CanRead() {
		t.Fatal("Error with capabilities")
	}

	if fmt.Sprintf("%x", id.Pub) != vector1.Pub {
		t.Fatal("Wrong public key!")
	}

	if fmt.Sprintf("%x", id.TorrentInfoHash[:]) != vector1.TIH {
		t.Fatalf("Wrong torrent info hash!\n\texpected\t%s\n\tactual  \t%x\n",
			vector1.TIH, id.TorrentInfoHash)
	}
}

func TestNewFromStringReadStore(t *testing.T) {
	id, err := NewFromString(vector1.RS)
	if err != nil {
		t.Fatal(err)
	}
	if id.CanWrite() || !id.CanRead() {
		t.Fatal("Error with capabilities")
	}
	if fmt.Sprintf("%x", id.Pub) != vector1.Pub {
		t.Fatalf("Wrong public key! Expected %s, got %x\n", vector1.Pub, id.Pub)
	}

	if fmt.Sprintf("%x", id.Enc) != vector1.Enc {
		t.Fatalf("Wrong encryption key!\n\tExpected\t%s\n\tgot\t%x\n",
			vector1.Enc, id.Enc)
	}
	if fmt.Sprintf("%x", id.TorrentInfoHash[:]) != vector1.TIH {
		t.Fatalf("Wrong torrent info hash!\n\texpected\t%s\n\tactual  \t%x\n",
			vector1.TIH, id.TorrentInfoHash)
	}
}

// Test that the public key from 1 is correctly interpreted
func TestNewFromStringDerive(t *testing.T) {

	id, err := NewFromString(vector1.WRS)
	if err != nil {
		t.Fatal(err)
	}

	id2, err := NewFromString(id.ReadStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id.Pub[:], id2.Pub[:]) {
		t.Fatal("Public keys are different!")
	}

	if !bytes.Equal(id.Enc[:], id2.Enc[:]) {
		t.Fatalf("Encryption keys are different!\n\tfrom WRS:\t%x\n\tfrom RS:\t%x\n", id.Enc,
			id2.Enc)
	}

}
