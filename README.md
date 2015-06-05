Rakoshare [![Gobuild Download](http://gobuild.io/badge/github.com/rakoo/rakoshare/download.png)](http://gobuild.io/github.com/rakoo/rakoshare)
=========

Rakoshare is a sharing tool to synchronize a folder on multiple
computers with no configuration.

It was inspired by Bittorrent Sync and aims to be an Open Source
alternative. Internally it reuses the Bittorrent protocol to build a
more dynamic protocol.

Rakoshare is forked off of
[Taipei-Torrent](https://github.com/jackpal/Taipei-Torrent), a pure go
Bittorrent application.

Current Status
--------------

Working but unstable. Anything, both in the implementation or the
protocol, can change at any time (although the general idea should
    remain the same)

Rakoshare might currently eat your data if you're not cautious.

Tested on Linux.

- [x] On-the-fly encryption with [spiped](https://github.com/dchest/spipe)
- [ ] Capabilities on the model of Tahoe-LAFS
  - [x] ReadWrite capability
  - [x] Read capability
  - [ ] Store capability (ie keep data on disk without being able to read it)

Development Roadmap
-------------------

*  Encryption

    * Encryption of the data at-rest is desirable to send data to
    untrusted servers.

*  Speed
    * Rakoshare is currently naive in how the folders are checked, there
      is room for improvement on this side

Download, Install, and Build Instructions
-----------------------------------------

1. Download and install the Go tools from http://golang.org

2. Use the "go" command to download, install, and build the Rakoshare
app:

    go get github.com/rakoo/rakoshare

Usage Instructions
------------------

1. Create a share session:

  `$ ./rakoshare gen -dir <somedir>`

  The result will be a list of different ids, each with a different
  capability:

  ```
  WriteReadStore: AgpUdzGDo14K7hmkce3pLUXWq3nf1sJfXmmyzeN9vmrNpfRxwZUQjuvhPaZvnysJtB6K2M8tT6f1vvriTko2hP38
       ReadStore: ovWviooStXvVpgY7Vrd2FryM8pwDHNL9TyUWdsxAUBUv
           Store: 2CSNWUTbN9arXsF37Eu9HmYbUeD5VukpRsgQnCwRAnMyg
  ```

  It will also create a session file in your ~/.local/share/rakoshare
  folder. You shouldn't need to look at it.

2. Start sharing content:

  `$ ./rakoshare share -id <one of the previous id>`

  If you use the WriteReadStore id, any change you make will be
  propagated. If you use the ReadStore id, no local changes will be
  propagated. The Store id currently works as a ReadStore id.

  Send an Id to someone else, and start sharing !

2. If you receive content from someone else, start receiving and sharing content:

  `$ ./rakoshare share -id <the id you received> -dir <where to store data>`

For more info:

    rakoshare help

Demo !
------

I'm fetching a slew of pics from /r/EarthPorn with
[redditEarthPorn](https://github.com/rakoo/redditEarthPorn) that are
then shared with a rakoshare instance that I run on my server. Use id
evK5HghADU2GrgM2NWFyKsRUj4ymkhoagz9DXgpW6EcN to receive a bunch of
gorgeous images, courtesy of reddit users ! The folder is updated
approximately every hour, and I will do my best to cap the size of the
folder to a reasonable amount (expect ~ 70 MB).

To test locally:

  `$ ./rakoshare share -id evK5HghADU2GrgM2NWFyKsRUj4ymkhoagz9DXgpW6EcN -dir /some/dir/`

Note that this id is read-only, meaning you can't modify what is
shared by everyone.

Third-party Packages
--------------------

http://code.google.com/p/bencode-go - Bencode encoder/decoder

https://github.com/zeebo/bencode    - Another Bencode encoder/decoder

http://code.google.com/p/go-nat-pmp - NAT-PMP firewall client

https://github.com/hailiang/gosocks - SOCKS5 proxy support

https://github.com/nictuku/dht      - Distributed Hash Table

https://github.com/nictuku/nettools - Network utilities

https://github.com/dchest/spipe     - pure-go [spiped](https://www.tarsnap.com/spiped.html) implementation

https://github.com/codegangsta/cli  - CLI application builder package

Related Projects
----------------

https://github.com/jackpal/Taipei-Torrent is the base of rakoshare

Discussion
----------

Please open issues on the github tracker
(https://github.com/rakoo/rakoshare/issues), or discuss over the mailing
list: rakoshare served-by googlegroups.com
