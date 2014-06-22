package id

import "testing"

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

	id, err := NewFromString("Pm1ox83SwF41LdgbCQNMKNdWzDT8KcSW8HAeraWCNvdL")
	if err != nil {
		t.Fatal(err)
	}
	if id == nil {
		t.Fatal("Couldn't decode string!")
	}
	if !id.CanWrite() || !id.CanRead() {
		t.Fatal("Error with capabilities")
	}

}
