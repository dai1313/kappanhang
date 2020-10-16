package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/nonoo/kappanhang/log"
)

var conn *net.UDPConn

func send(d []byte) {
	_, err := conn.Write(d)
	if err != nil {
		log.Fatal(err)
	}
}

func read() ([]byte, error) {
	err := conn.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		log.Fatal(err)
	}

	b := make([]byte, 1500)
	n, _, err := conn.ReadFromUDP(b)
	if err != nil {
		if err, ok := err.(net.Error); ok && !err.Timeout() {
			log.Fatal(err)
		}
	}
	return b[:n], err
}

func main() {
	log.Init()
	parseArgs()

	hostPort := fmt.Sprint(connectAddress, ":", connectPort)
	log.Print("connecting to ", hostPort)
	raddr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		log.Fatal(err)
	}
	conn, err = net.DialUDP("udp", nil, raddr)
	if err != nil {
		log.Fatal(err)
	}

	// Constructing the local session ID by combining the local IP address and port.
	laddr := conn.LocalAddr().(*net.UDPAddr)
	localSID := binary.BigEndian.Uint32(laddr.IP[len(laddr.IP)-4:])<<16 | uint32(laddr.Port&0xffff)
	log.Debugf("using session id %.8x", localSID)

	send([]byte{0x10, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0x00, 0x00, 0x00})
	// send([]byte{0x15, 0x00, 0x00, 0x00, 0x07, 0x00, 0x01, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x3e, 0x10, 0x00})
	// send([]byte{0x10, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0x00, 0x00, 0x00})
	// send([]byte{0x15, 0x00, 0x00, 0x00, 0x07, 0x00, 0x02, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0x00, 0x00, 0x00, 0x00, 0x70, 0x3e, 0x10, 0x00})
	// send([]byte{0x10, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0x00, 0x00, 0x00})

	var remoteSID uint32
	for {
		r, _ := read()
		if bytes.Equal(r[:8], []byte{0x10, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}) {
			remoteSID = binary.BigEndian.Uint32(r[8:12])
			break
		}
	}

	log.Debugf("got remote session id %.8x", remoteSID)

	send([]byte{0x10, 0x00, 0x00, 0x00, 0x06, 0x00, 0x01, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID)})
	send([]byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID),
		0x00, 0x00, 0x00, 0x70, 0x01, 0x00, 0x00, 0x22,
		0x00, 0x00, 0x09, 0x27, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2b, 0x3f, 0x55, 0x5c, 0x00, 0x00, 0x00, 0x00, // username: beer
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2b, 0x3f, 0x55, 0x5c, 0x3f, 0x25, 0x77, 0x58, // pass: beerbeer
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x69, 0x63, 0x6f, 0x6d, 0x2d, 0x70, 0x63, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	var sendSeq uint8
	var lastReceivedSeq uint8
	var receivedSeq bool
	var lastPingAt time.Time
	var errCount int
	for {
		r, err := read()
		if err != nil {
			errCount++
			if errCount > 5 {
				log.Fatal("timeout")
			}
			log.Error("stream break detected")
		}
		errCount = 0

		if len(r) == 96 && bytes.Equal(r[:8], []byte{0x60, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00}) {
			if bytes.Equal(r[48:52], []byte{0xff, 0xff, 0xff, 0xfe}) {
				log.Fatal("invalid user/password")
			} else {
				log.Print("auth ok")
			}
		}
		if bytes.Equal(r[:6], []byte{0x00, 0x00, 0x00, 0x00, 0x07, 0x00}) {
			gotSeq := r[6]
			if receivedSeq && lastReceivedSeq+1 != gotSeq {
				log.Error("packet loss detected")
			}
			lastReceivedSeq = gotSeq
			receivedSeq = true
		}

		if time.Since(lastPingAt) >= time.Second {
			// 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14
			// 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x21, 0x00, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14
			send([]byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x00, sendSeq, 0x00, byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID), byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID)})

			// 0x00, 0x00, 0x00, 0x00, 0x07, 0x00, 0x86, 0x08, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14, 0x00, 0x08, 0xdf, 0x10, 0x00
			// 0x00, 0x00, 0x00, 0x00, 0x07, 0x00, 0x87, 0x08, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14, 0x00, 0x6c, 0xdf, 0x10, 0x00
			send([]byte{0x00, 0x00, 0x00, 0x00, 0x07, 0x00, sendSeq, 0x08, byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID), byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x00, 0xd0, 0xdf, 0x10, 0x00})

			// 0x00, 0x00, 0x00, 0x00, 0x07, 0x00, 0x1c, 0x00, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14, 0x01, 0xb2, 0x48, 0x10, 0x00
			// 0x00, 0x00, 0x00, 0x00, 0x07, 0x00, 0x1d, 0x00, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14, 0x01, 0x17, 0x49, 0x10, 0x00
			// 0x00, 0x00, 0x00, 0x00, 0x07, 0x00, 0x1e, 0x00, 0xf0, 0xa4, 0xf5, 0x9d, 0xad, 0x89, 0x2b, 0x14, 0x01, 0x7c, 0x49, 0x10, 0x00
			send([]byte{0x00, 0x00, 0x00, 0x00, 0x07, 0x00, sendSeq, 0x00, byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID), byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), 0x01, 0xe1, 0x49, 0x10, 0x00})

			sendSeq++

			//send([]byte{0x10, 0x00, 0x00, 0x00, 0x06, 0x00, 0x01, 0x00, byte(localSID >> 24), byte(localSID >> 16), byte(localSID >> 8), byte(localSID), byte(remoteSID >> 24), byte(remoteSID >> 16), byte(remoteSID >> 8), byte(remoteSID)})
			lastPingAt = time.Now()
		}
	}
}
