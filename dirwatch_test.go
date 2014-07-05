package main

import (
	"crypto/sha1"
	"io/ioutil"
	"os"
	"testing"
)

type testVector struct {
	dir             string
	expectedTorrent string
}

var vecs = []testVector{
	{
		dir:             "testData/dsl-4.4.10.iso",
		expectedTorrent: "testData/dsl-1M.torrent",
	},
	{
		dir:             "testData/1Mrandom",
		expectedTorrent: "testData/1Mrandom.torrent",
	},
}

func TestTorrentify(t *testing.T) {
	if testing.Short() {
		t.Skip("This test requires the iso")
	}

	for _, vec := range vecs {
		if _, err := os.Stat("testData/dsl-4.4.10.iso"); err != nil && os.IsNotExist(err) {
			t.Fatal("You need to download the iso relative to a.torrent to run this test")
		}

		actualMeta, err := createMeta(vec.dir)
		if err != nil {
			t.Fatal(err)
		}
		actualPieces := split(actualMeta.Info.Pieces)

		expected, err := ioutil.ReadFile(vec.expectedTorrent)
		if err != nil {
			t.Fatal(err)
		}

		expectedMeta, err := NewMetaInfoFromContent(expected)
		if err != nil {
			t.Fatal(err)
		}
		expectedPieces := split(expectedMeta.Info.Pieces)

		if len(expectedPieces) != len(actualPieces) {
			t.Fatalf("Invalid length! Expected %d piece(s), got %d\n", len(expectedPieces), len(actualPieces))
		}

		for i, exp := range expectedPieces {
			if actualPieces[i] != exp {
				t.Fatalf("%x - %x\n", exp, actualPieces[i])
			}
		}

	}
}

func split(pieces string) []string {
	out := make([]string, len(pieces)/sha1.Size)
	for i := 0; i < len(out); i++ {
		from := i * sha1.Size
		to := (i + 1) * sha1.Size
		if i == len(out)-1 {
			to = len(pieces)
		}
		out[i] = pieces[from:to]
	}
	return out
}
