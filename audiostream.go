package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"

	"github.com/nonoo/kappanhang/log"
)

const audioTimeoutDuration = 3 * time.Second
const rxSeqBufLength = 100 * time.Millisecond

type audioStream struct {
	common streamCommon

	audio audioStruct

	deinitNeededChan   chan bool
	deinitFinishedChan chan bool

	timeoutTimer         *time.Timer
	receivedAudio        bool
	lastReceivedAudioSeq uint16
	rxSeqBuf             seqBuf
	rxSeqBufEntryChan    chan seqBufEntry

	audioSendSeq uint16
}

// sendPart1 expects 1364 bytes of PCM data.
func (s *audioStream) sendPart1(pcmData []byte) error {
	err := s.common.send(append([]byte{0x6c, 0x05, 0x00, 0x00, 0x00, 0x00, byte(s.audioSendSeq), byte(s.audioSendSeq >> 8),
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID),
		0x80, 0x00, byte((s.audioSendSeq - 1) >> 8), byte(s.audioSendSeq - 1), 0x00, 0x00, byte(len(pcmData) >> 8), byte(len(pcmData))},
		pcmData...))
	if err != nil {
		return err
	}
	s.audioSendSeq++
	return nil
}

// sendPart2 expects 556 bytes of PCM data.
func (s *audioStream) sendPart2(pcmData []byte) error {
	err := s.common.send(append([]byte{0x44, 0x02, 0x00, 0x00, 0x00, 0x00, byte(s.audioSendSeq), byte(s.audioSendSeq >> 8),
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID),
		0x80, 0x00, byte((s.audioSendSeq - 1) >> 8), byte(s.audioSendSeq - 1), 0x00, 0x00, byte(len(pcmData) >> 8), byte(len(pcmData))},
		pcmData...))
	if err != nil {
		return err
	}
	s.audioSendSeq++
	return nil
}

func (s *audioStream) sendRetransmitRequest(seqNum uint16) error {
	p := []byte{0x10, 0x00, 0x00, 0x00, 0x01, 0x00, byte(seqNum), byte(seqNum >> 8),
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID)}
	if err := s.common.send(p); err != nil {
		return err
	}
	if err := s.common.send(p); err != nil {
		return err
	}
	return nil
}

type seqNumRange [2]uint16

func (s *audioStream) sendRetransmitRequestForRanges(seqNumRanges []seqNumRange) error {
	seqNumBytes := make([]byte, len(seqNumRanges)*4)
	for i := 0; i < len(seqNumRanges); i++ {
		seqNumBytes[i*2] = byte(seqNumRanges[i][0])
		seqNumBytes[i*2+1] = byte(seqNumRanges[i][0] >> 8)
		seqNumBytes[i*2+2] = byte(seqNumRanges[i][1])
		seqNumBytes[i*2+3] = byte(seqNumRanges[i][1] >> 8)
	}
	p := append([]byte{0x18, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00,
		byte(s.common.localSID >> 24), byte(s.common.localSID >> 16), byte(s.common.localSID >> 8), byte(s.common.localSID),
		byte(s.common.remoteSID >> 24), byte(s.common.remoteSID >> 16), byte(s.common.remoteSID >> 8), byte(s.common.remoteSID)},
		seqNumBytes...)
	if err := s.common.send(p); err != nil {
		return err
	}
	if err := s.common.send(p); err != nil {
		return err
	}
	return nil
}

func (s *audioStream) handleRxSeqBufEntry(e seqBufEntry) {
	gotSeq := uint16(e.seq)
	if s.receivedAudio {
		expectedSeq := s.lastReceivedAudioSeq + 1
		if expectedSeq != gotSeq {
			var missingPkts int
			if gotSeq > expectedSeq {
				missingPkts = int(gotSeq) - int(expectedSeq)
			} else {
				missingPkts = int(gotSeq) + 65536 - int(expectedSeq)
			}
			log.Error("lost ", missingPkts, " audio packets")
		}
	}
	s.lastReceivedAudioSeq = gotSeq
	s.receivedAudio = true

	s.audio.play <- e.data
}

func (s *audioStream) handleAudioPacket(r []byte) {
	if s.timeoutTimer != nil {
		s.timeoutTimer.Stop()
		s.timeoutTimer.Reset(audioTimeoutDuration)
	}

	gotSeq := binary.LittleEndian.Uint16(r[6:8])
	err := s.rxSeqBuf.add(seqNum(gotSeq), r[24:])
	if err != nil {
		log.Error(err)
	}
}

func (s *audioStream) handleRead(r []byte) {
	if len(r) >= 580 && (bytes.Equal(r[:6], []byte{0x6c, 0x05, 0x00, 0x00, 0x00, 0x00}) || bytes.Equal(r[:6], []byte{0x44, 0x02, 0x00, 0x00, 0x00, 0x00})) {
		s.handleAudioPacket(r)
	}
}

func (s *audioStream) loop() {
	for {
		select {
		case r := <-s.common.readChan:
			s.handleRead(r)
		case <-s.timeoutTimer.C:
			reportError(errors.New("audio stream timeout, try rebooting the radio"))
		case e := <-s.rxSeqBufEntryChan:
			s.handleRxSeqBufEntry(e)
		case d := <-s.audio.rec:
			if err := s.sendPart1(d[:1364]); err != nil {
				reportError(err)
			}
			if err := s.sendPart2(d[1364:1920]); err != nil {
				reportError(err)
			}
		case <-s.deinitNeededChan:
			s.deinitFinishedChan <- true
			return
		}
	}
}

func (s *audioStream) start(devName string) error {
	if err := s.audio.init(devName); err != nil {
		return err
	}

	if err := s.common.sendPkt3(); err != nil {
		return err
	}
	if err := s.common.waitForPkt4Answer(); err != nil {
		return err
	}
	if err := s.common.sendPkt6(); err != nil {
		return err
	}
	if err := s.common.waitForPkt6Answer(); err != nil {
		return err
	}

	log.Print("stream started")

	s.timeoutTimer = time.NewTimer(audioTimeoutDuration)

	s.common.pkt7.startPeriodicSend(&s.common, 1, false)

	s.audioSendSeq = 1

	s.deinitNeededChan = make(chan bool)
	s.deinitFinishedChan = make(chan bool)
	go s.loop()
	return nil
}

func (s *audioStream) init() error {
	if err := s.common.init("audio", 50003); err != nil {
		return err
	}
	s.rxSeqBufEntryChan = make(chan seqBufEntry)
	s.rxSeqBuf.init(rxSeqBufLength, 0xffff, 0, s.rxSeqBufEntryChan)
	return nil
}

func (s *audioStream) deinit() {
	if s.deinitNeededChan != nil {
		s.deinitNeededChan <- true
		<-s.deinitFinishedChan
	}
	if s.timeoutTimer != nil {
		s.timeoutTimer.Stop()
	}
	s.common.deinit()
	s.rxSeqBuf.deinit()
	s.audio.deinit()
}
