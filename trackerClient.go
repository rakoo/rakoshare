package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nictuku/nettools"
	"github.com/zeebo/bencode"
)

// Code to talk to trackers.
// Implements BEP 12 Multitracker Metadata Extension

type ClientStatusReport struct {
	Event      string
	InfoHash   string
	PeerId     string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
}

type trackerClient struct {
	trackerInfoChan chan *TrackerResponse
	announceList    [][]string
	failedTrackers  map[string]struct{}
}

func NewTrackerClient(announce string, announceList [][]string) trackerClient {
	if announce != "" && announceList == nil {
		// Convert the plain announce into an announceList to simplify logic
		announceList = [][]string{[]string{announce}}
	}

	if announceList != nil {
		announceList = shuffleAnnounceList(announceList)
	}
	tic := make(chan *TrackerResponse)
	return trackerClient{
		trackerInfoChan: tic,
		announceList:    announceList,
		failedTrackers:  make(map[string]struct{}),
	}
}

func (tc trackerClient) Announce(report ClientStatusReport) {
	go func() {
		tr := tc.queryTrackers(report)
		if tr != nil {
			tc.trackerInfoChan <- tr
		}
	}()
}

// Deep copy announcelist and shuffle each level.
func shuffleAnnounceList(announceList [][]string) (result [][]string) {
	result = make([][]string, len(announceList))
	for i, level := range announceList {
		result[i] = shuffleAnnounceListLevel(level)
	}
	return
}

func shuffleAnnounceListLevel(level []string) (shuffled []string) {
	items := len(level)
	shuffled = make([]string, items)
	perm := rand.Perm(items)
	for i, v := range perm {
		shuffled[v] = level[i]
	}
	return
}

func (tc trackerClient) queryTrackers(report ClientStatusReport) (tr *TrackerResponse) {
	for _, level := range tc.announceList {
		for i, tracker := range level {
			if _, ok := tc.failedTrackers[tracker]; ok {
				continue
			}
			var err error
			tr, err = queryTracker(report, tracker)
			if err == nil {
				// Move successful tracker to front of slice for next announcement
				// cycle.
				copy(level[1:i+1], level[0:i])
				level[0] = tracker
				return
			} else {
				log.Println("Couldn't contact", tracker, ": ", err)
				if _, ok := tc.failedTrackers[tracker]; !ok {
					log.Printf("Blacklisting failed tracker %s", tracker)
				}
				tc.failedTrackers[tracker] = struct{}{}
			}
		}
	}
	if len(tc.announceList) > 0 && len(tc.announceList[0]) > 0 {
		log.Println("Error: Did not successfully contact a tracker:", tc.announceList)
	}
	return
}

func queryTracker(report ClientStatusReport, trackerUrl string) (tr *TrackerResponse, err error) {
	// We sometimes indicate www.domain.com:port/path, it should be
	// automatically detected
	if !strings.HasPrefix(trackerUrl, "http") {
		trackerUrl = "http://" + trackerUrl
	}

	u, err := url.Parse(trackerUrl)
	if err != nil {
		log.Println("Error: Invalid announce URL(", trackerUrl, "):", err)
		return
	}

	uq := u.Query()
	uq.Add("info_hash", report.InfoHash)
	uq.Add("peer_id", report.PeerId)
	uq.Add("port", strconv.Itoa(report.Port))
	uq.Add("uploaded", strconv.FormatInt(report.Uploaded, 10))
	uq.Add("downloaded", strconv.FormatInt(report.Downloaded, 10))
	uq.Add("left", strconv.FormatInt(report.Left, 10))
	uq.Add("compact", "1")

	// Don't report IPv6 address, the user might prefer to keep
	// that information private when communicating with IPv4 hosts.
	if false {
		ipv6Address, err := findLocalIPV6AddressFor(u.Host)
		if err == nil {
			log.Println("our ipv6", ipv6Address)
			uq.Add("ipv6", ipv6Address)
		}
	}

	if report.Event != "" {
		uq.Add("event", report.Event)
	}

	// This might reorder the existing query string in the Announce url
	// This might break some broken trackers that don't parse URLs properly.

	u.RawQuery = uq.Encode()

	tr, err = getTrackerInfo(u.String())
	if tr == nil || err != nil {
		log.Println("Error: Could not fetch tracker info:", err)
	} else if tr.FailureReason != "" {
		log.Println("Error: Tracker returned failure reason:", tr.FailureReason)
		err = fmt.Errorf("tracker failure %s", tr.FailureReason)
	}
	return
}

func findLocalIPV6AddressFor(hostAddr string) (local string, err error) {
	// Figure out our IPv6 address to talk to a given host.
	host, hostPort, err := net.SplitHostPort(hostAddr)
	if err != nil {
		host = hostAddr
		hostPort = "1234"
	}
	dummyAddr := net.JoinHostPort(host, hostPort)
	log.Println("Looking for host ", dummyAddr)
	conn, err := net.Dial("udp6", dummyAddr)
	if err != nil {
		log.Println("No IPV6 for host ", host, err)
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr()
	local, _, err = net.SplitHostPort(localAddr.String())
	if err != nil {
		local = localAddr.String()
	}
	return
}

type TrackerResponse struct {
	FailureReason  string "failure reason"
	WarningMessage string "warning message"
	Interval       time.Duration
	MinInterval    time.Duration "min interval"
	TrackerId      string        "tracker id"
	Complete       int
	Incomplete     int
	PeersRaw       string `bencode:"peers"`
	Peers          []string
	Peers6Raw      string `bencode:"peers6"`
	Peers6         []string
}

func getTrackerInfo(url string) (tr *TrackerResponse, err error) {
	r, err := proxyHttpGet(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		data, _ := ioutil.ReadAll(r.Body)
		reason := "Bad Request " + string(data)
		log.Println(reason)
		err = errors.New(reason)
		return
	}

	var tr2 TrackerResponse
	err = bencode.NewDecoder(r.Body).Decode(&tr2)
	r.Body.Close()
	if err != nil {
		return
	}

	// Decode peers
	if len(tr2.PeersRaw) > 0 {
		const peerLen = 6
		nPeers := len(tr2.PeersRaw) / peerLen
		// log.Println("Tracker gave us", nPeers, "peers")
		tr2.Peers = make([]string, nPeers)
		for i := 0; i < nPeers; i++ {
			peer := nettools.BinaryToDottedPort(tr2.PeersRaw[i*peerLen : (i+1)*peerLen])
			tr2.Peers[i/peerLen] = peer
		}
	}

	// Decode peers6

	if len(tr2.Peers6Raw) > 0 {
		const peerLen = 18
		nPeers := len(tr2.Peers6Raw) / peerLen
		// log.Println("Tracker gave us", nPeers, "IPv6 peers")
		tr2.Peers6 = make([]string, nPeers)
		for i := 0; i < nPeers; i++ {
			peerEntry := tr2.Peers6Raw[i*peerLen : (i+1)*peerLen]
			host := net.IP(peerEntry[0:16])
			port := int((uint(peerEntry[16]) << 8) | uint(peerEntry[17]))
			peer := net.JoinHostPort(host.String(), strconv.Itoa(port))
			tr2.Peers6[i] = peer
		}
	}

	tr = &tr2
	return
}
