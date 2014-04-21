package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/zeebo/bencode"
)

func TestTorrentify(t *testing.T) {
	if testing.Short() {
		t.Skip("This test requires the iso")
	}

	if _, err := os.Stat("testData/dsl-4.4.10.iso"); err != nil && os.IsNotExist(err) {
		t.Fatal("You need to download the iso relative to a.torrent to run this test")
	}

	meta, err := createMeta("testData")
	if err != nil {
		t.Fatal(err)
	}

	out, _ := os.Create("outf")
	bencode.NewEncoder(out).Encode(meta)

	expected := "f6dbbaa994438a0160d2b6f0f6129ba8a9f2d3ab"
	if fmt.Sprintf("%x", meta.InfoHash) != expected {
		t.Fatalf("Wrong infohash; expected %s, got %x",
			expected,
			meta.InfoHash)
	}

}
