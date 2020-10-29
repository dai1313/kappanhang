package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
)

const controlStreamPort = 50001
const serialStreamPort = 50002
const audioStreamPort = 50003

type controlStream struct {
	common streamCommon
	serial serialStream
	audio  audioStream

	deinitNeededChan   chan bool
	deinitFinishedChan chan bool

	authInnerSendSeq uint16
	authID           [6]byte
	gotAuthID        bool
	authOk           bool

	a8replyID    [16]byte
	gotA8ReplyID bool

	serialAndAudioStreamOpened bool
	deinitializing             bool

	secondAuthTimer              *time.Timer
	requestSerialAndAudioTimeout *time.Timer
	reauthTimeoutTimer           *time.Timer
}

func (s *controlStream) sendPktLogin() error {
	// The reply to the auth packet will contain a 6 bytes long auth ID with the first 2 bytes set to our ID.
	authStartID := []byte{0x63, 0x00}
	p := []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID),
		0x00, 0x00, 0x00, 0x70, 0x01, 0x00, 0x00, byte(s.authInnerSendSeq),
		byte(s.authInnerSendSeq >> 8), 0x00, authStartID[0], authStartID[1], 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2b, 0x3f, 0x55, 0x5c, 0x00, 0x00, 0x00, 0x00, // username: beer
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2b, 0x3f, 0x55, 0x5c, 0x3f, 0x25, 0x77, 0x58, // pass: beerbeer
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x69, 0x63, 0x6f, 0x6d, 0x2d, 0x70, 0x63, 0x00, // icom-pc in plain text
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := s.common.pkt0.sendTrackedPacket(&s.common, p); err != nil {
		return err
	}

	s.authInnerSendSeq++
	return nil
}

func (s *controlStream) sendPktAuth(magic byte) error {
	// Example request from PC:  0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0d, 0x00,
	//                           0xbb, 0x41, 0x3f, 0x2b, 0xe6, 0xb2, 0x7b, 0x7b,
	//                           0x00, 0x00, 0x00, 0x30, 0x01, 0x05, 0x00, 0x02,
	//                           0x00, 0x00, 0x5d, 0x37, 0x12, 0x82, 0x3b, 0xde,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00
	// Example reply from radio: 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0e, 0x00,
	//                           0xe6, 0xb2, 0x7b, 0x7b, 0xbb, 0x41, 0x3f, 0x2b,
	//                           0x00, 0x00, 0x00, 0x30, 0x02, 0x05, 0x00, 0x02,
	//                           0x00, 0x00, 0x5d, 0x37, 0x12, 0x82, 0x3b, 0xde,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                           0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00

	p := []byte{0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID),
		0x00, 0x00, 0x00, 0x30, 0x01, magic, 0x00, byte(s.authInnerSendSeq),
		byte(s.authInnerSendSeq >> 8), 0x00, s.authID[0], s.authID[1], s.authID[2], s.authID[3], s.authID[4], s.authID[5],
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := s.common.pkt0.sendTrackedPacket(&s.common, p); err != nil {
		return err
	}
	s.authInnerSendSeq++
	return nil
}

func (s *controlStream) sendRequestSerialAndAudio() error {
	log.Debug("requesting serial and audio stream")

	txSeqBufLengthMs := uint16(txSeqBufLength.Milliseconds())

	p := []byte{0x90, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID),
		0x00, 0x00, 0x00, 0x80, 0x01, 0x03, 0x00, byte(s.authInnerSendSeq),
		byte(s.authInnerSendSeq >> 8), 0x00, s.authID[0], s.authID[1], s.authID[2], s.authID[3], s.authID[4], s.authID[5],
		s.a8replyID[0], s.a8replyID[1], s.a8replyID[2], s.a8replyID[3], s.a8replyID[4], s.a8replyID[5], s.a8replyID[6], s.a8replyID[7],
		s.a8replyID[8], s.a8replyID[9], s.a8replyID[10], s.a8replyID[11], s.a8replyID[12], s.a8replyID[13], s.a8replyID[14], s.a8replyID[15],
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x49, 0x43, 0x2d, 0x37, 0x30, 0x35, 0x00, 0x00, // IC-705 in plain text
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2b, 0x3f, 0x55, 0x5c, 0x00, 0x00, 0x00, 0x00, // username: beer
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x01, 0x04, 0x04, 0x00, 0x00, byte(audioSampleRate >> 8), byte(audioSampleRate & 0xff),
		0x00, 0x00, byte(audioSampleRate >> 8), byte(audioSampleRate & 0xff),
		0x00, 0x00, byte(serialStreamPort >> 8), byte(serialStreamPort & 0xff),
		0x00, 0x00, byte(audioStreamPort >> 8), byte(audioStreamPort & 0xff), 0x00, 0x00,
		byte(txSeqBufLengthMs >> 8), byte(txSeqBufLengthMs & 0xff), 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := s.common.pkt0.sendTrackedPacket(&s.common, p); err != nil {
		return err
	}

	s.authInnerSendSeq++

	return nil
}

func (s *controlStream) sendRequestSerialAndAudioIfPossible() {
	if !s.serialAndAudioStreamOpened && s.authOk && s.gotA8ReplyID {
		time.AfterFunc(time.Second, func() {
			if err := s.sendRequestSerialAndAudio(); err != nil {
				reportError(err)
			}
		})
	}
}

func (s *controlStream) handleRead(r []byte) error {
	switch len(r) {
	case 168:
		if bytes.Equal(r[:6], []byte{0xa8, 0x00, 0x00, 0x00, 0x00, 0x00}) {
			// Example answer from radio:
			// 0xa8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00,
			// 0x01, 0x13, 0x11, 0x18, 0x38, 0xff, 0x55, 0x7d,
			// 0x00, 0x00, 0x00, 0x98, 0x02, 0x02, 0x00, 0x07,
			// 0x00, 0x00, 0x7f, 0x91, 0x00, 0x00, 0x4f, 0x0d,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x01, 0x93, 0x8a, 0x01, 0x24, 0x17, 0x64,
			// 0xbc, 0x4b, 0xa3, 0xa0, 0x13, 0x58, 0x41, 0x04,
			// 0x58, 0x2d, 0x49, 0x43, 0x2d, 0x37, 0x30, 0x35,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x49, 0x43, 0x4f, 0x4d, 0x5f, 0x56,
			// 0x41, 0x55, 0x44, 0x49, 0x4f, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x3f, 0x3f, 0xa4, 0x01, 0xff, 0x01,
			// 0xff, 0x01, 0x01, 0x01, 0x00, 0x00, 0x4b, 0x00,
			// 0x01, 0x50, 0x00, 0xb8, 0x0b, 0x00, 0x00, 0x00
			copy(s.a8replyID[:], r[66:82])
			s.gotA8ReplyID = true
		}
	case 64:
		if bytes.Equal(r[:6], []byte{0x40, 0x00, 0x00, 0x00, 0x00, 0x00}) {
			// Example answer from radio:   0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
			// 0x33, 0x60, 0xd4, 0xe5, 0xf4, 0x67, 0x86, 0xe1,
			// 0x00, 0x00, 0x00, 0x30, 0x02, 0x05, 0x00, 0x02,
			// 0x00, 0x00, 0x35, 0x34, 0x76, 0x11, 0xb9, 0xd0,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00

			s.reauthTimeoutTimer.Stop()

			log.Debug("auth ok")

			if r[21] == 0x05 { // Answer for our second auth?
				s.authOk = true
				s.secondAuthTimer.Stop()
				s.sendRequestSerialAndAudioIfPossible()
			}
		}
	case 80:
		if bytes.Equal(r[:6], []byte{0x50, 0x00, 0x00, 0x00, 0x00, 0x00}) {
			// Example answer from radio: 0x50, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x00,
			//							  0x86, 0x1f, 0x2f, 0xcc, 0x03, 0x03, 0x89, 0x29,
			//							  0x00, 0x00, 0x00, 0x40, 0x02, 0x03, 0x00, 0x52,
			//							  0x00, 0x00, 0xf8, 0xad, 0x06, 0x8d, 0xda, 0x7b,
			//							  0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10,
			//							  0x80, 0x00, 0x00, 0x90, 0xc7, 0x0e, 0x86, 0x01,
			//							  0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00,
			//							  0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			//							  0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			//							  0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00

			if bytes.Equal(r[48:51], []byte{0xff, 0xff, 0xff}) {
				if !s.serialAndAudioStreamOpened {
					return errors.New("auth failed, try rebooting the radio")
				} else {
					return errors.New("auth failed")
				}
			}
			if bytes.Equal(r[48:51], []byte{0x00, 0x00, 0x00}) && r[64] == 0x01 {
				return errors.New("got radio disconnected")
			}
		}
	case 144:
		if !s.serialAndAudioStreamOpened && bytes.Equal(r[:6], []byte{0x90, 0x00, 0x00, 0x00, 0x00, 0x00}) && r[96] == 1 {
			// Example answer:
			// 0x90, 0x00, 0x00, 0x00, 0x00, 0x00, 0x19, 0x00,
			// 0xc6, 0x5f, 0x6f, 0x0c, 0x5f, 0x8b, 0x1e, 0x89,
			// 0x00, 0x00, 0x00, 0x80, 0x03, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x31, 0x30, 0x31, 0x47, 0x39, 0x07,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10,
			// 0x80, 0x00, 0x00, 0x90, 0xc7, 0x0e, 0x86, 0x01,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x49, 0x43, 0x2d, 0x37, 0x30, 0x35, 0x00, 0x00, // IC-705 in plain text
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x01, 0x00, 0x00, 0x00, 0x69, 0x63, 0x6f, 0x6d,
			// 0x2d, 0x70, 0x63, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// 0x00, 0x00, 0x00, 0x00, 0xc0, 0xa8, 0x03, 0x03,
			// 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00

			s.secondAuthTimer.Stop()
			s.requestSerialAndAudioTimeout.Stop()

			devName := parseNullTerminatedString(r[64:])
			log.Print("got serial and audio request success, device name: ", devName)

			// Stuff can change in the meantime because of a previous login...
			s.common.remoteSID = binary.BigEndian.Uint32(r[8:12])
			s.common.localSID = binary.BigEndian.Uint32(r[12:16])
			copy(s.authID[:], r[26:32])
			s.gotAuthID = true

			if err := s.serial.init(devName); err != nil {
				return errors.New("serial/" + err.Error())
			}

			if err := s.audio.init(devName); err != nil {
				return errors.New("audio/" + err.Error())
			}

			s.serialAndAudioStreamOpened = true
			statusLog.startPeriodicPrint()

			startCmdIfNeeded()
		}
	}
	return nil
}

func (s *controlStream) loop() {
	netstat.reset()

	s.secondAuthTimer = time.NewTimer(time.Second)
	s.reauthTimeoutTimer = time.NewTimer(0)
	<-s.reauthTimeoutTimer.C

	reauthTicker := time.NewTicker(25 * time.Second)

	for {
		select {
		case <-s.secondAuthTimer.C:
			if err := s.sendPktAuth(0x05); err != nil {
				reportError(err)
			}
			log.Debug("second auth sent...")
		case r := <-s.common.readChan:
			if !s.deinitializing {
				if err := s.handleRead(r); err != nil {
					reportError(err)
				}
			}
		case <-reauthTicker.C:
			log.Debug("sending auth")
			s.reauthTimeoutTimer.Reset(3 * time.Second)
			if err := s.sendPktAuth(0x05); err != nil {
				reportError(err)
			}
		case <-s.reauthTimeoutTimer.C:
			log.Error("auth timeout, audio/serial stream may stop")
		case <-s.deinitNeededChan:
			s.deinitFinishedChan <- true
			return
		}
	}
}

func (s *controlStream) init() error {
	log.Debug("init")

	if err := s.common.init("control", controlStreamPort); err != nil {
		return err
	}

	if err := s.common.start(); err != nil {
		return err
	}

	s.common.pkt7.startPeriodicSend(&s.common, 2, false)

	s.common.pkt0.init(&s.common)
	if err := s.sendPktLogin(); err != nil {
		return err
	}

	log.Debug("expecting login answer")
	// Example success auth packet: 0x60, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00,
	//                              0xe6, 0xb2, 0x7b, 0x7b, 0xbb, 0x41, 0x3f, 0x2b,
	//                              0x00, 0x00, 0x00, 0x50, 0x02, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x5d, 0x37, 0x12, 0x82, 0x3b, 0xde,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x46, 0x54, 0x54, 0x48, 0x00, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	//                              0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00
	r, err := s.common.expect(96, []byte{0x60, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00})
	if err != nil {
		return err
	}
	if bytes.Equal(r[48:52], []byte{0xff, 0xff, 0xff, 0xfe}) {
		return errors.New("invalid user/password")
	}

	copy(s.authID[:], r[26:32])
	s.gotAuthID = true
	if err := s.sendPktAuth(0x02); err != nil {
		return err
	}
	log.Debug("login ok, first auth sent...")

	s.common.pkt0.startPeriodicSend(&s.common)

	s.requestSerialAndAudioTimeout = time.AfterFunc(5*time.Second, func() {
		reportError(errors.New("login/serial/audio request timeout"))
	})

	s.deinitNeededChan = make(chan bool)
	s.deinitFinishedChan = make(chan bool)
	go s.loop()
	return nil
}

func (s *controlStream) deinit() {
	s.deinitializing = true
	s.serialAndAudioStreamOpened = false
	statusLog.stopPeriodicPrint()

	if s.deinitNeededChan != nil {
		s.deinitNeededChan <- true
		<-s.deinitFinishedChan
	}
	if s.requestSerialAndAudioTimeout != nil {
		s.requestSerialAndAudioTimeout.Stop()
		s.requestSerialAndAudioTimeout = nil
	}

	if s.gotAuthID && s.common.gotRemoteSID && s.common.conn != nil {
		log.Debug("sending deauth")
		_ = s.sendPktAuth(0x01)
		// Waiting a little bit to make sure the radio can send retransmit requests.
		time.Sleep(500 * time.Millisecond)
	}

	s.common.deinit()
	s.serial.deinit()
	s.audio.deinit()
}
