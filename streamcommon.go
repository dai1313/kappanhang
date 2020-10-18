package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/nonoo/kappanhang/log"
)

const expectTimeoutDuration = time.Second

type streamCommon struct {
	name             string
	conn             *net.UDPConn
	localSID         uint32
	remoteSID        uint32
	gotRemoteSID     bool
	readChan         chan []byte
	readerClosedChan chan bool

	pkt7 pkt7Type
}

func (s *streamCommon) send(d []byte) {
	_, err := s.conn.Write(d)
	if err != nil {
		log.Fatal(err)
	}
}

func (s *streamCommon) read() ([]byte, error) {
	b := make([]byte, 1500)
	n, _, err := s.conn.ReadFromUDP(b)
	return b[:n], err
}

func (s *streamCommon) reader() {
	for {
		r, err := s.read()
		if err != nil {
			break
		}
		if s.pkt7.isPkt7(r) {
			s.pkt7.handle(s, r)
		}

		s.readChan <- r
	}
	s.readerClosedChan <- true
}

func (s *streamCommon) tryReceivePacket(timeout time.Duration, packetLength, matchStartByte int, b []byte) []byte {
	var r []byte
	timer := time.NewTimer(timeout)
	for {
		select {
		case r = <-s.readChan:
		case <-timer.C:
			return nil
		}

		if len(r) == packetLength && bytes.Equal(r[matchStartByte:len(b)+matchStartByte], b) {
			break
		}
	}
	return r
}

func (s *streamCommon) expect(packetLength int, b []byte) []byte {
	r := s.tryReceivePacket(expectTimeoutDuration, packetLength, 0, b)
	if r == nil {
		log.Fatal(s.name + "/expect timeout")
	}
	return r
}

func (s *streamCommon) open(name string, portNumber int) {
	s.name = name
	hostPort := fmt.Sprint(connectAddress, ":", portNumber)
	log.Print(s.name+"/connecting to ", hostPort)
	raddr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		log.Fatal(err)
	}

	// Use the same local and remote port. The radio does not handle different ports well.
	l := net.UDPAddr{
		Port: portNumber,
	}
	s.conn, err = net.DialUDP("udp", &l, raddr)
	if err != nil {
		log.Fatal(err)
	}

	// Constructing the local session ID by combining the local IP address and port.
	laddr := s.conn.LocalAddr().(*net.UDPAddr)
	s.localSID = binary.BigEndian.Uint32(laddr.IP[len(laddr.IP)-4:])<<16 | uint32(laddr.Port&0xffff)
	log.Debugf(s.name+"/using session id %.8x", s.localSID)

	_, err = rand.Read(s.pkt7.randIDBytes[:])
	if err != nil {
		log.Fatal(err)
	}

	s.readChan = make(chan []byte)
	s.readerClosedChan = make(chan bool)
	go s.reader()

	if r := s.pkt7.tryReceive(300*time.Millisecond, s); s.pkt7.isPkt7(r) {
		s.remoteSID = binary.BigEndian.Uint32(r[8:12])
		s.gotRemoteSID = true
		log.Print(s.name + "/closing running stream")
		s.sendDisconnect()
		time.Sleep(time.Second)

		s.close()
		s.remoteSID = 0
		s.gotRemoteSID = false
		s.pkt7.sendSeq = 0
		s.pkt7.lastConfirmedSeq = 0
		s.open(name, portNumber)
	}
}

func (s *streamCommon) close() {
	s.conn.Close()

	// Depleting the read channel.
	var finished bool
	for !finished {
		select {
		case <-s.readChan:
		default:
			finished = true
		}
	}

	// Waiting for the reader to finish.
	<-s.readerClosedChan
}

func (s *streamCommon) sendPkt3() {
	s.send([]byte{0x10, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00,
		byte(s.localSID >> 24), byte(s.localSID >> 16), byte(s.localSID >> 8), byte(s.localSID),
		byte(s.remoteSID >> 24), byte(s.remoteSID >> 16), byte(s.remoteSID >> 8), byte(s.remoteSID)})
}

func (s *streamCommon) waitForPkt4Answer() {
	log.Debug(s.name + "/expecting a pkt4 answer")
	// Example answer from radio: 0x10, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x8c, 0x7d, 0x45, 0x7a, 0x1d, 0xf6, 0xe9, 0x0b
	r := s.expect(16, []byte{0x10, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00})
	s.remoteSID = binary.BigEndian.Uint32(r[8:12])
	s.gotRemoteSID = true

	log.Debugf(s.name+"/got remote session id %.8x", s.remoteSID)
}

func (s *streamCommon) sendPkt6() {
	s.send([]byte{0x10, 0x00, 0x00, 0x00, 0x06, 0x00, 0x01, 0x00,
		byte(s.localSID >> 24), byte(s.localSID >> 16), byte(s.localSID >> 8), byte(s.localSID),
		byte(s.remoteSID >> 24), byte(s.remoteSID >> 16), byte(s.remoteSID >> 8), byte(s.remoteSID)})
}

func (s *streamCommon) waitForPkt6Answer() {
	log.Debug(s.name + "/expecting pkt6 answer")
	// Example answer from radio: 0x10, 0x00, 0x00, 0x00, 0x06, 0x00, 0x01, 0x00, 0xe8, 0xd0, 0x44, 0x50, 0xa0, 0x61, 0x39, 0xbe
	s.expect(16, []byte{0x10, 0x00, 0x00, 0x00, 0x06, 0x00, 0x01, 0x00})
}

func (s *streamCommon) sendDisconnect() {
	if !s.gotRemoteSID {
		return
	}

	s.send([]byte{0x10, 0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00,
		byte(s.localSID >> 24), byte(s.localSID >> 16), byte(s.localSID >> 8), byte(s.localSID),
		byte(s.remoteSID >> 24), byte(s.remoteSID >> 16), byte(s.remoteSID >> 8), byte(s.remoteSID)})
}