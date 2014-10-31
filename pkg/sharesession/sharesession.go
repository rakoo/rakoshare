package sharesession

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	Q_SELECT_TORRENT     = `SELECT torrent FROM meta`
	Q_SELECT_INFOHASH    = `SELECT infohash FROM meta`
	Q_SELECT_LASTMODTIME = `SELECT lastmodtime FROM meta`
	Q_SELECT_IHMESSAGE   = `SELECT ihmessage FROM meta`
	Q_INSERT_TORRENT     = `UPDATE meta SET torrent=?, infohash=?, lastmodtime=?`
	Q_INSERT_IHMESSAGE   = `UPDATE meta SET ihmessage=?`
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
		_, err := db.Exec("CREATE TABLE meta(torrent string, infohash string, lastmodtime string, ihmessage string)")
		if err == nil {
			_, err = db.Exec(`INSERT INTO meta VALUES ("", "", "", "")`)
		}
		if err != nil {
			return nil, err
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
		return time.Now()
	}
	return parsed
}

func (s *Session) GetCurrentIHMessage() string {
	return s.getFromMeta(Q_SELECT_IHMESSAGE)
}

func (s *Session) getFromMeta(query string) string {
	var value string
	err := s.db.QueryRow(query).Scan(&value)
	if err != nil {
		log.Println("Can't get from sql: ", err)
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
