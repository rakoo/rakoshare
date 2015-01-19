package main

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/rakoo/rakoshare/pkg/id"
	"github.com/rakoo/rakoshare/pkg/sharesession"
)

func Generate(target, workDir string) error {
	tmpId, err := id.New()
	if err != nil {
		return err
	}
	fmt.Printf("WriteReadStore:\t%s\n     ReadStore:\t%s\n         Store:\t%s\n",
		tmpId.WRS(), tmpId.RS(), tmpId.S())

	dbFile := filepath.Join(workDir, hex.EncodeToString(tmpId.Infohash)+".sql")
	session, err := sharesession.New(dbFile)
	if err != nil {
		return err
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	return session.SaveSession(target, tmpId)
}
