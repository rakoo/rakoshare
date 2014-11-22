package sharesession

import (
	"database/sql"
	"time"

	"github.com/rakoo/rakoshare/pkg/id"

	_ "github.com/mattn/go-sqlite3"
)

var (
	Q_SELECT_TORRENT     = `SELECT torrent FROM meta`
	Q_SELECT_INFOHASH    = `SELECT infohash FROM meta`
	Q_SELECT_LASTMODTIME = `SELECT lastmodtime FROM meta`
	Q_SELECT_IHMESSAGE   = `SELECT ihmessage FROM meta`
	Q_SELECT_FOLDER      = `SELECT folder FROM meta`
	Q_INSERT_TORRENT     = `UPDATE meta SET torrent=?, infohash=?, lastmodtime=?`
	Q_INSERT_IHMESSAGE   = `UPDATE meta SET ihmessage=?`
)

var (
	CREATESTRINGS = []string{
		`CREATE TABLE meta(
			torrent string,
			infohash string,
			lastmodtime string,
			ihmessage string,
			folder string,
			wrs string,
			rs string,
			s string
		)`,
		//`INSERT INTO meta VALUES ("", "", "", "", "")`,
	}
)

type Session struct {
	db *sql.DB
}

func New(path string) (*Session, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	err = db.Ping()
	if err != nil {
		return nil, err
	}

	var ph string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='meta'").Scan(&ph)
	if err == sql.ErrNoRows {
		for _, str := range CREATESTRINGS {
			_, err := db.Exec(str)
			if err != nil {
				return nil, err
			}
		}
	}

	return &Session{db}, nil
}

func (s *Session) GetCurrentInfohash() string {
	return s.getFromMeta(Q_SELECT_INFOHASH)
}

func (s *Session) GetCurrentTorrent() string {
	return s.getFromMeta(Q_SELECT_TORRENT)
}

func (s *Session) GetLastModTime() time.Time {
	t := s.getFromMeta(Q_SELECT_LASTMODTIME)
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		// Return the beginning of time, so that all files are after this
		// date
		return time.Unix(0, 0)
	}
	return parsed
}

func (s *Session) GetCurrentIHMessage() string {
	return s.getFromMeta(Q_SELECT_IHMESSAGE)
}

func (s *Session) GetTarget() string {
	return s.getFromMeta(Q_SELECT_FOLDER)
}

func (sess *Session) GetShareId() id.Id {
	var wrs, rs, s string
	err := sess.db.QueryRow(`SELECT wrs, rs, s FROM meta`).Scan(&wrs, &rs, &s)
	if err != nil {
		return id.Id{}
	}
	var ret id.Id
	switch {
	case wrs != "":
		ret, _ = id.NewFromString(wrs)
	case rs != "":
		ret, _ = id.NewFromString(rs)
	case s != "":
		ret, _ = id.NewFromString(s)
	}

	return ret
}

func (s *Session) getFromMeta(query string) string {
	var value string
	err := s.db.QueryRow(query).Scan(&value)
	if err != nil {
		//log.Printf("Can't get from sql: %s", err)
		return ""
	}

	return value
}

func (s *Session) SaveTorrent(torrent []byte, infohash, lastModTime string) error {
	_, err := s.db.Exec(Q_INSERT_TORRENT, torrent, infohash, lastModTime)
	return err
}

func (s *Session) SaveIHMessage(mess []byte) error {
	_, err := s.db.Exec(Q_INSERT_IHMESSAGE, mess)
	return err
}

func (s *Session) SaveSession(target string, theid id.Id) error {
	_, err := s.db.Exec(`INSERT INTO meta (folder, wrs, rs, s) VALUES (?, ?, ?, ?)`,
		target, theid.WRS(), theid.RS(), theid.S())
	return err
}
