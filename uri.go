package main

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	errNoBtih = errors.New("Magnet URI xt parameter missing the 'urn:btih:' prefix. Not a bittorrent hash link?")
)

type Magnet struct {
	InfoHashes []string
	Names      []string
}

func parseMagnet(s string) (mag Magnet, err error) {
	// References:
	// - http://bittorrent.org/beps/bep_0009.html
	// - http://en.wikipedia.org/wiki/Magnet_URI_scheme
	//
	// Example bittorrent magnet link:
	//
	// => magnet:?xt=urn:btih:bbb6db69965af769f664b6636e7914f8735141b3&dn=Ubuntu-12.04-desktop-i386.iso
	//
	// xt: exact topic.
	//   ~ urn: uniform resource name.
	//   ~ btih: bittorrent infohash.
	// dn: display name (optional).
	// tr: address tracker (optional).
	u, err := url.Parse(s)
	if err != nil {
		return
	}
	xts, ok := u.Query()["xt"]
	if !ok {
		err = errors.New(fmt.Sprintf("Magnet URI missing the 'xt' argument" + s))
		return
	}

	infoHashes := make([]string, 0, len(xts))
	for _, xt := range xts {
		s := strings.Split(xt, "urn:btih:")
		if len(s) != 2 {
			err = errNoBtih
			return
		}

		ih := s[1]

		// TODO: support base32 encoded hashes, if they still exist.
		if len(ih) != sha1.Size*2 { // hex format.
			err = errors.New(fmt.Sprintf("Magnet URI contains infohash with unexpected length. Wanted %d, got %d: %v", sha1.Size, len(ih), ih))
			return
		}

		infoHashes = append(infoHashes, s[1])
	}

	var names []string
	n, ok := u.Query()["dn"]
	if ok {
		names = n
	}

	mag = Magnet{
		InfoHashes: infoHashes,
		Names:      names,
	}

	return
}
