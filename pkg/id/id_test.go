package id

import (
	"encoding/hex"
	"testing"
)

type vec struct {
	WRS string
	RS  string
	S   string
}

var (
	vector1 = vec{
		WRS: "B76BogapG9DGadhsuUd1ikteJKo7iAnMKNjHtAf8KA626SnWuuiVCQaUftG5cGRWUCGFGegTnmRRourTX6WwHor8",
		RS:  "j4vDxToJfvocsC1BL6pjoYDP8DjugsUWHVyQ6kFHQmh2",
		S:   "2KqymCfosgEQi3Q6zYLSoqsJSz4mYnxFXNAbMMtPqjSrb",
	}
)

func TestNew(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatal("There should be no error for creating an id!")
	}

	if !id.CanWrite() {
		t.Fatal("should be able to write")
	}

	if !id.CanRead() {
		t.Fatal("should be able to read")
	}
}

func TestNewFromStringWRS(t *testing.T) {
	_, err := NewFromString("GARBAGE")
	if err == nil {
		t.Fatal("Garbage shouldn't be accepted!")
	}

	id, err := NewFromString(vector1.WRS)
	if err != nil {
		t.Fatal("Couldn't create id: ", err)
	}

	if !id.CanWrite() {
		t.Fatal("id should be able to write")
	}

	hexpriv := hex.EncodeToString(id.Priv[:])
	expectedhexpriv := "f96bd82f536527c87d7fde59106f9a8573ea6ec183f67d6509bd666aa2f0da8e710bc5f0cd2538d35e093b9041e083d0072f9ba80e5a7f59e08c029afc788cd9"
	if hexpriv != expectedhexpriv {
		t.Fatalf("Invalid priv: expected %s, got %s", expectedhexpriv, hexpriv)
	}

	if !id.CanRead() {
		t.Fatal("id should be able to read")
	}

	hexpub := hex.EncodeToString(id.Pub[:])
	expectedhexpub := "710bc5f0cd2538d35e093b9041e083d0072f9ba80e5a7f59e08c029afc788cd9"
	if hexpub != expectedhexpub {
		t.Fatalf("Invalid pub: expected %s, got %s", expectedhexpub, hexpub)
	}

	hexpsk := hex.EncodeToString(id.Psk[:])
	expectedhexpsk := "75c4442eb37c07ad5df9f131a62046d71d89c3e7210f1e8916b93e2deba611d0"
	if hexpsk != expectedhexpsk {
		t.Fatalf("Invalid psk: expected %s, got %s", expectedhexpsk, hexpsk)
	}

	ih := id.Infohash()
	hexih := hex.EncodeToString(ih[:])
	expectedih := "ddb859685f8ea3f7f8e81c9980f4447a0b90d246"
	if hexih != expectedih {
		t.Fatalf("Invalid infohash: expected %s, got %s", expectedih, hexih)
	}
}
