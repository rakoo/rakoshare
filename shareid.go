package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"log"
)

var (
	errNoRWID = errors.New("no read-write id for this share")
)

type ShareID struct {
	rw  string
	ro  string
	pub string
}

func NewShareID(in string) ShareID {
	if len(in) != 40 {
		log.Fatalf("incorrect format; expected 40 chars, got %d\n", len(in))
	}

	s := ShareID{}
	if in[:3] == "RW-" {
		s.rw = in
	} else {
		s.ro = in
	}

	return s
}

func (s ShareID) PublicID() string {
	if s.pub != "" {
		return s.pub
	}

	s.pub = s.derive(s.ReadOnlyID())
	return s.pub
}

func (s ShareID) ReadOnlyID() string {
	if s.ro != "" {
		return s.ro
	}
	if s.rw == "" {
		log.Fatal("Called ReadOnlyID() on a ShareID that doesn't have a ro and rw")
	}

	s.ro = s.derive(s.rw)
	return s.ro
}

func (s ShareID) ReadWriteID() (string, error) {
	if s.rw == "" {
		return "", errNoRWID
	}
	return s.rw, nil
}

func (s ShareID) HighestPrivilegeID() string {
	if s.rw != "" {
		return s.rw
	}
	return s.ro
}

func (s ShareID) derive(from string) string {
	sum := sha1.Sum([]byte(from))
	return hex.EncodeToString(sum[:])
}
