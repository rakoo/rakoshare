package sharesession

import (
	"database/sql"
	"log"
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

		`CREATE TABLE peers(
			hostport string primary key,
			valid_until string
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

	session := &Session{db}
	go session.watchPeers()
	return session, nil
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

func (s *Session) SavePeer(peer string, shouldKeep func(peer string) bool) error {
	validUntil := time.Now().Add(24 * time.Hour)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO peers VALUES (?, ?)`, peer, validUntil.Format(time.RFC3339))
	if err != nil {
		return err
	}

	time.AfterFunc(validUntil.Sub(time.Now()), func() {
		if !shouldKeep(peer) {
			s.deletePeerOrLog(peer, validUntil.Format(time.RFC3339))
		}
	})
	return nil
}

func (s *Session) GetPeers() (peers []string) {
	peersWithValidity, err := s.getPeersWithValidity()
	if err != nil {
		return
	}

	peers = make([]string, len(peersWithValidity))
	for i, pwv := range peersWithValidity {
		peers[i] = pwv.hostport
	}
	return peers
}

type peer struct {
	hostport   string
	validUntil string
}

func (s *Session) getPeersWithValidity() (peers []peer, err error) {
	rows, err := s.db.Query(`SELECT * FROM peers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	peers = make([]peer, 0)
	for rows.Next() {
		var hostport string
		var validUntil string

		if errs := rows.Scan(&hostport, &validUntil); errs != nil {
			err = errs
			break
		}

		peers = append(peers, peer{hostport, validUntil})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return peers, err
}

func (s *Session) watchPeers() {
	peers, err := s.getPeersWithValidity()
	if err != nil {
		log.Fatal(err)
	}

	for _, p := range peers {
		if eol, err := time.Parse(time.RFC3339, p.validUntil); err != nil {
			s.deletePeerOrLog(p.hostport, p.validUntil)
		} else {
			time.AfterFunc(eol.Sub(time.Now()), func() { s.deletePeerOrLog(p.hostport, p.validUntil) })
		}
	}
}

func (s *Session) deletePeerOrLog(hostport string, validUntil string) {
	_, errDel := s.db.Exec(`DELETE FROM peers WHERE hostport = ? AND valid_until = ?`, hostport, validUntil)
	if errDel != nil {
		log.Printf("Couldn't delete (%s, %s): %s\n", hostport, validUntil, errDel)
	}
}
