// Package test provides a stateful mock SSC (tape) target for CI testing.
// It implements minimal iSCSI framing (login + SCSI Command dispatch) and
// responds to SSC commands with realistic tape-drive behavior: position
// tracking, filemark storage, and EOM enforcement.
//
// This package is intentionally self-contained and does NOT import from
// the v1.0 test/ package (which uses internal/ packages). It implements
// just enough iSCSI to work with uiscsi.Session.
package test

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
)

// MockTapeDrive is a stateful mock SSC target that simulates a tape drive.
// It maintains an in-memory byte buffer as virtual tape media, tracks the
// read/write head position, stores filemark locations, and enforces EOM.
type MockTapeDrive struct {
	mu           sync.Mutex
	media        []byte // virtual tape contents
	mediaSize    int    // total capacity (for EOM)
	position     int    // current head position in media
	written      int    // bytes actually written to media (end of data marker)
	filemarks    []int  // positions where filemarks exist
	deviceType   uint8  // INQUIRY device type (default 0x01 = tape)
	eomThreshold int    // position at which EOM early warning triggers
	blockSize    int    // configured block size (0 = variable, >0 = fixed); set by MODE SELECT

	errorQueues     map[uint8][]injectedError     // keyed by CDB opcode
	shortReadQueues map[uint8][]injectedShortRead // keyed by CDB opcode

	listener net.Listener
	addr     string
	done     chan struct{}
	wg       sync.WaitGroup
}

type injectedError struct {
	senseKey uint8
	asc      uint8
	ascq     uint8
}

type injectedShortRead struct {
	actualLen int
}

// NewMockTapeDrive creates a new mock tape drive with the given media capacity
// in bytes. It starts a TCP listener on localhost and accepts iSCSI connections.
func NewMockTapeDrive(mediaSize int) *MockTapeDrive {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Sprintf("mock tape: listen failed: %v", err))
	}

	m := &MockTapeDrive{
		media:        make([]byte, mediaSize),
		mediaSize:    mediaSize,
		filemarks:    make([]int, 0),
		deviceType:   0x01, // sequential access (tape) by default
		eomThreshold: mediaSize * 9 / 10, // 90% of capacity
		listener:     ln,
		addr:         ln.Addr().String(),
		done:         make(chan struct{}),
	}

	m.wg.Add(1)
	go m.acceptLoop()

	return m
}

// Addr returns the listener address (host:port) for dialing.
func (m *MockTapeDrive) Addr() string {
	return m.addr
}

// Position returns the current head position.
func (m *MockTapeDrive) Position() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.position
}

// SetDeviceType overrides the INQUIRY device type returned by the mock.
// Use 0x00 (disk) to test ErrNotTape handling.
func (m *MockTapeDrive) SetDeviceType(dt uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deviceType = dt
}

// SetEOMThreshold sets the position at which EOM early warning triggers.
// Writes that cross this threshold succeed but return CHECK CONDITION with
// EOM sense. Writes that exceed mediaSize return VOLUME OVERFLOW.
func (m *MockTapeDrive) SetEOMThreshold(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eomThreshold = n
}

// Written returns the number of bytes written (end of data marker).
func (m *MockTapeDrive) Written() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.written
}

// Close stops the listener and waits for all goroutines to exit.
func (m *MockTapeDrive) Close() {
	close(m.done)
	m.listener.Close()
	m.wg.Wait()
}

// InjectError queues a one-shot sense error consumed on the next SCSI
// command matching the given opcode. Multiple calls for the same opcode
// queue errors in FIFO order.
func (m *MockTapeDrive) InjectError(opcode, senseKey, asc, ascq uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errorQueues == nil {
		m.errorQueues = make(map[uint8][]injectedError)
	}
	m.errorQueues[opcode] = append(m.errorQueues[opcode], injectedError{senseKey, asc, ascq})
}

// InjectFilemark places a filemark at the given byte position in the mock
// tape media. The filemark is consumed by READ (like existing filemarks)
// and navigated by SPACE.
func (m *MockTapeDrive) InjectFilemark(position int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filemarks = append(m.filemarks, position)
}

// InjectShortRead queues a one-shot short read for the given opcode.
// The next READ matching that opcode returns only actualLen bytes with
// ILI sense (if SILI=false) including the residue in INFORMATION.
func (m *MockTapeDrive) InjectShortRead(opcode uint8, actualLen int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shortReadQueues == nil {
		m.shortReadQueues = make(map[uint8][]injectedShortRead)
	}
	m.shortReadQueues[opcode] = append(m.shortReadQueues[opcode], injectedShortRead{actualLen})
}

// consumeInjectedError pops the first queued error for the given opcode.
// Returns the error and true if one was queued, or zero value and false.
func (m *MockTapeDrive) consumeInjectedError(opcode uint8) (injectedError, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.errorQueues[opcode]
	if len(q) == 0 {
		return injectedError{}, false
	}
	e := q[0]
	m.errorQueues[opcode] = q[1:]
	return e, true
}

// consumeInjectedShortRead pops the first queued short read for the given opcode.
func (m *MockTapeDrive) consumeInjectedShortRead(opcode uint8) (injectedShortRead, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.shortReadQueues[opcode]
	if len(q) == 0 {
		return injectedShortRead{}, false
	}
	e := q[0]
	m.shortReadQueues[opcode] = q[1:]
	return e, true
}

func (m *MockTapeDrive) acceptLoop() {
	defer m.wg.Done()
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			select {
			case <-m.done:
				return
			default:
				log.Printf("mock tape: accept error: %v", err)
				return
			}
		}
		m.wg.Add(1)
		go m.serveConn(conn)
	}
}

func (m *MockTapeDrive) serveConn(conn net.Conn) {
	defer m.wg.Done()
	defer conn.Close()

	// Phase 1: iSCSI Login
	if err := m.handleLogin(conn); err != nil {
		log.Printf("mock tape: login failed: %v", err)
		return
	}

	// Phase 2: Full-feature phase -- dispatch SCSI commands
	var statSN uint32 = 1
	for {
		bhs, data, err := readPDU(conn)
		if err != nil {
			return // connection closed
		}

		opcode := bhs[0] & 0x3F
		switch opcode {
		case 0x01: // SCSI Command
			m.handleSCSICommand(conn, bhs, data, &statSN)
		case 0x06: // Logout Request
			m.handleLogout(conn, bhs, &statSN)
			return
		case 0x00: // NOP-Out
			m.handleNOPOut(conn, bhs, &statSN)
		default:
			log.Printf("mock tape: unhandled opcode 0x%02X", opcode)
		}
	}
}

// handleLogin processes iSCSI login negotiation. Handles security (AuthMethod=None)
// and operational negotiation phases, transitioning to full-feature phase.
func (m *MockTapeDrive) handleLogin(conn net.Conn) error {
	for {
		bhs, data, err := readPDU(conn)
		if err != nil {
			return fmt.Errorf("read login PDU: %w", err)
		}

		opcode := bhs[0] & 0x3F
		if opcode != 0x03 { // Login Request
			return fmt.Errorf("expected Login Request (0x03), got 0x%02X", opcode)
		}

		// Parse login request fields
		transit := bhs[1]&0x80 != 0
		csg := (bhs[1] >> 2) & 0x03
		nsg := bhs[1] & 0x03
		itt := binary.BigEndian.Uint32(bhs[16:20])
		cmdSN := binary.BigEndian.Uint32(bhs[24:28])

		// Parse ISID (bytes 8-13)
		var isid [6]byte
		copy(isid[:], bhs[8:14])

		// Parse text key-value pairs from data segment
		kvs := parseTextKV(data)

		switch csg {
		case 0: // Security Negotiation
			respKVs := make(map[string]string)
			for k := range kvs {
				if k == "AuthMethod" {
					respKVs["AuthMethod"] = "None"
				}
			}

			respData := encodeTextKV(respKVs)
			resp := makeLoginResp(isid, itt, cmdSN, csg, nsg, transit, respData, 0)
			if err := writePDU(conn, resp, respData); err != nil {
				return err
			}

			if transit && nsg == 3 {
				return nil // direct to full-feature phase
			}

		case 1: // Operational Negotiation
			respKVs := make(map[string]string)
			for k, v := range kvs {
				switch k {
				case "HeaderDigest", "DataDigest":
					respKVs[k] = "None"
				case "MaxRecvDataSegmentLength":
					respKVs[k] = "8192"
				default:
					respKVs[k] = v // echo back
				}
			}

			respData := encodeTextKV(respKVs)
			resp := makeLoginResp(isid, itt, cmdSN, csg, 3, true, respData, 1)
			if err := writePDU(conn, resp, respData); err != nil {
				return err
			}
			return nil // transition to full-feature phase
		}
	}
}

// handleSCSICommand dispatches SCSI commands by CDB opcode.
func (m *MockTapeDrive) handleSCSICommand(conn net.Conn, bhs [48]byte, data []byte, statSN *uint32) {
	itt := binary.BigEndian.Uint32(bhs[16:20])
	cmdSN := binary.BigEndian.Uint32(bhs[24:28])

	// CDB is at bytes 32-47 in the BHS
	var cdb [16]byte
	copy(cdb[:], bhs[32:48])

	cdbOpcode := cdb[0]

	// Check for injected errors before normal dispatch.
	if injErr, ok := m.consumeInjectedError(cdbOpcode); ok {
		sense := makeFixedSense(injErr.senseKey, false, false, false, injErr.asc, injErr.ascq)
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	switch cdbOpcode {
	case 0x00: // TEST UNIT READY
		m.handleTUR(conn, itt, cmdSN, statSN)
	case 0x12: // INQUIRY
		m.handleInquiry(conn, itt, cmdSN, statSN, cdb)
	case 0x05: // READ BLOCK LIMITS
		m.handleReadBlockLimits(conn, itt, cmdSN, statSN)
	case 0x0A: // WRITE(6)
		m.handleWrite(conn, itt, cmdSN, statSN, cdb, data)
	case 0x08: // READ(6)
		m.handleRead(conn, itt, cmdSN, statSN, cdb)
	case 0x10: // WRITE FILEMARKS(6)
		m.handleWriteFilemarks(conn, itt, cmdSN, statSN, cdb)
	case 0x01: // REWIND
		m.handleRewind(conn, itt, cmdSN, statSN)
	case 0x34: // READ POSITION
		m.handleReadPosition(conn, itt, cmdSN, statSN)
	case 0x1A: // MODE SENSE(6)
		m.handleModeSense6(conn, itt, cmdSN, statSN)
	case 0x15: // MODE SELECT(6)
		m.handleModeSelect6(conn, itt, cmdSN, statSN, data)
	case 0x11: // SPACE(6)
		m.handleSPACE(conn, itt, cmdSN, statSN, cdb)
	default:
		// Unknown CDB -- send CHECK CONDITION with ILLEGAL REQUEST
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, nil) // CHECK CONDITION
	}
}

// handleTUR responds to TEST UNIT READY with GOOD status.
func (m *MockTapeDrive) handleTUR(conn net.Conn, itt uint32, cmdSN uint32, statSN *uint32) {
	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
}

// handleInquiry returns a standard 36-byte INQUIRY response.
// Device type defaults to 0x01 (tape) but can be overridden via SetDeviceType.
func (m *MockTapeDrive) handleInquiry(conn net.Conn, itt uint32, cmdSN uint32, statSN *uint32, cdb [16]byte) {
	allocLen := uint16(cdb[3])<<8 | uint16(cdb[4])
	if allocLen == 0 {
		allocLen = 36
	}

	m.mu.Lock()
	dt := m.deviceType
	m.mu.Unlock()

	inqData := make([]byte, 36)
	inqData[0] = dt // Device type (default: sequential access / tape)
	inqData[1] = 0x80 // Removable media
	inqData[2] = 0x06 // SPC-4
	inqData[3] = 0x02 // Response data format 2
	inqData[4] = 31   // Additional length (36-5)
	copy(inqData[8:16], padString("UISCSI", 8))
	copy(inqData[16:32], padString("VirtualTape", 16))
	copy(inqData[32:36], padString("0001", 4))

	sendLen := int(allocLen)
	if sendLen > len(inqData) {
		sendLen = len(inqData)
	}

	sendDataIn(conn, itt, cmdSN, statSN, inqData[:sendLen], 0x00)
}

// handleReadBlockLimits returns a 6-byte READ BLOCK LIMITS response.
func (m *MockTapeDrive) handleReadBlockLimits(conn net.Conn, itt uint32, cmdSN uint32, statSN *uint32) {
	resp := make([]byte, 6)
	resp[0] = 0x00                                 // Granularity = 0
	resp[1] = 0x10                                 // Max block length bytes 1-3: 0x100000 = 1 MiB
	resp[2] = 0x00                                 // ...
	resp[3] = 0x00                                 // ...
	binary.BigEndian.PutUint16(resp[4:6], 0x0001)  // Min block length = 1

	sendDataIn(conn, itt, cmdSN, statSN, resp, 0x00)
}

// handleLogout responds to Logout Request.
func (m *MockTapeDrive) handleLogout(conn net.Conn, bhs [48]byte, statSN *uint32) {
	itt := binary.BigEndian.Uint32(bhs[16:20])
	cmdSN := binary.BigEndian.Uint32(bhs[24:28])

	sn := *statSN
	*statSN++

	var resp [48]byte
	resp[0] = 0x26 // Logout Response opcode
	resp[1] = 0x80 // Final
	binary.BigEndian.PutUint32(resp[16:20], itt)
	binary.BigEndian.PutUint32(resp[24:28], sn)          // StatSN
	binary.BigEndian.PutUint32(resp[28:32], cmdSN+1)     // ExpCmdSN
	binary.BigEndian.PutUint32(resp[32:36], cmdSN+10)    // MaxCmdSN

	writePDU(conn, resp, nil)
}

// handleNOPOut responds to NOP-Out with NOP-In.
func (m *MockTapeDrive) handleNOPOut(conn net.Conn, bhs [48]byte, statSN *uint32) {
	itt := binary.BigEndian.Uint32(bhs[16:20])
	cmdSN := binary.BigEndian.Uint32(bhs[24:28])

	sn := *statSN
	*statSN++

	var resp [48]byte
	resp[0] = 0x20 // NOP-In opcode
	resp[1] = 0x80 // Final
	binary.BigEndian.PutUint32(resp[16:20], itt)
	// TargetTransferTag = 0xFFFFFFFF
	binary.BigEndian.PutUint32(resp[20:24], 0xFFFFFFFF)
	binary.BigEndian.PutUint32(resp[24:28], sn)          // StatSN
	binary.BigEndian.PutUint32(resp[28:32], cmdSN+1)     // ExpCmdSN
	binary.BigEndian.PutUint32(resp[32:36], cmdSN+10)    // MaxCmdSN

	writePDU(conn, resp, nil)
}

// handleWrite processes a WRITE(6) command.
// It extracts transfer length from CDB bytes 2-4, handles FIXED mode
// (multiply by 512), enforces EOM/VOLUME OVERFLOW, and copies data to media.
func (m *MockTapeDrive) handleWrite(conn net.Conn, itt, cmdSN uint32, statSN *uint32, cdb [16]byte, data []byte) {
	xferLen := uint32(cdb[2])<<16 | uint32(cdb[3])<<8 | uint32(cdb[4])
	fixed := cdb[1]&0x01 != 0
	if fixed {
		bs := m.blockSize
		if bs == 0 {
			bs = 512 // fallback
		}
		xferLen *= uint32(bs)
	}

	writeLen := int(xferLen)
	if writeLen > len(data) {
		writeLen = len(data)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check VOLUME OVERFLOW (hard limit)
	if m.position+writeLen > m.mediaSize {
		sense := makeFixedSense(0x0D, false, true, false, 0x00, 0x00)
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	// Write the data
	copy(m.media[m.position:], data[:writeLen])
	m.position += writeLen
	if m.position > m.written {
		m.written = m.position
	}

	// Check EOM early warning (write succeeds but signals EOM)
	if m.position > m.eomThreshold {
		sense := makeFixedSense(0x00, false, true, false, 0x00, 0x02) // END-OF-PARTITION/MEDIUM DETECTED
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil) // GOOD
}

// handleRead processes a READ(6) command.
// It extracts transfer length from CDB bytes 2-4, handles FIXED mode,
// checks for filemarks and blank check, and sends data back.
func (m *MockTapeDrive) handleRead(conn net.Conn, itt, cmdSN uint32, statSN *uint32, cdb [16]byte) {
	xferLen := uint32(cdb[2])<<16 | uint32(cdb[3])<<8 | uint32(cdb[4])
	fixed := cdb[1]&0x01 != 0
	if fixed {
		bs := m.blockSize
		if bs == 0 {
			bs = 512 // fallback
		}
		xferLen *= uint32(bs)
	}

	// Check for injected short read before acquiring the lock.
	// consumeInjectedShortRead has its own lock.
	if inj, ok := m.consumeInjectedShortRead(cdb[0]); ok {
		m.mu.Lock()
		actualLen := inj.actualLen
		available := m.written - m.position
		if actualLen > available {
			actualLen = available
		}
		readLen := int(xferLen)
		if actualLen > readLen {
			actualLen = readLen
		}
		m.position += actualLen
		m.mu.Unlock()
		// SILI=false: return CHECK CONDITION with ILI sense and residue
		residue := int(xferLen) - actualLen
		sense := makeFixedSenseWithInfo(0x00, false, false, true, 0x00, 0x00, uint32(residue))
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if position is at a filemark. Consume it — remove from list
	// so subsequent reads proceed to the next record. Real drives advance
	// past the filemark on read.
	for i, fmPos := range m.filemarks {
		if fmPos == m.position {
			m.filemarks = append(m.filemarks[:i], m.filemarks[i+1:]...)
			sense := makeFixedSense(0x00, true, false, false, 0x00, 0x01) // FILEMARK
			sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
			return
		}
	}

	// Check blank check (reading past written data)
	if m.position >= m.written {
		sense := makeFixedSense(0x08, false, false, false, 0x00, 0x00) // BLANK CHECK
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	// Read available data
	available := m.written - m.position
	readLen := int(xferLen)
	if readLen > available {
		readLen = available
	}

	readData := make([]byte, readLen)
	copy(readData, m.media[m.position:m.position+readLen])
	m.position += readLen

	sendDataIn(conn, itt, cmdSN, statSN, readData, 0x00)
}

// handleWriteFilemarks processes a WRITE FILEMARKS(6) command.
// It records filemark positions at the current head position.
func (m *MockTapeDrive) handleWriteFilemarks(conn net.Conn, itt, cmdSN uint32, statSN *uint32, cdb [16]byte) {
	count := uint32(cdb[2])<<16 | uint32(cdb[3])<<8 | uint32(cdb[4])

	m.mu.Lock()
	defer m.mu.Unlock()

	for range count {
		m.filemarks = append(m.filemarks, m.position)
	}

	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil) // GOOD
}

// handleRewind processes a REWIND command.
// It sets the head position to 0. The IMMED bit is ignored in the mock.
func (m *MockTapeDrive) handleRewind(conn net.Conn, itt, cmdSN uint32, statSN *uint32) {
	m.mu.Lock()
	m.position = 0
	m.mu.Unlock()

	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil) // GOOD
}

// handleReadPosition processes a READ POSITION (short form) command.
// Returns a 20-byte response with BOP flag and current block position.
func (m *MockTapeDrive) handleReadPosition(conn net.Conn, itt, cmdSN uint32, statSN *uint32) {
	m.mu.Lock()
	pos := m.position
	m.mu.Unlock()

	resp := make([]byte, 20)
	if pos == 0 {
		resp[0] = 0x80 // BOP=1
	}
	// First block location at bytes 4-7 (big-endian uint32).
	binary.BigEndian.PutUint32(resp[4:8], uint32(pos))

	sendDataIn(conn, itt, cmdSN, statSN, resp, 0x00)
}

// handleModeSense6 processes a MODE SENSE(6) command.
// Returns a 12-byte response: 4-byte header + 8-byte block descriptor
// reflecting the current configured block size.
func (m *MockTapeDrive) handleModeSense6(conn net.Conn, itt, cmdSN uint32, statSN *uint32) {
	m.mu.Lock()
	bs := m.blockSize
	m.mu.Unlock()

	resp := make([]byte, 12)
	resp[0] = 11   // Mode Data Length (12 - 1)
	resp[3] = 8    // Block Descriptor Length
	// Byte 4: density code (0 = default)
	// Bytes 5-7: number of blocks (0)
	// Byte 8: reserved
	// Bytes 9-11: block length
	resp[9] = byte(bs >> 16)
	resp[10] = byte(bs >> 8)
	resp[11] = byte(bs)

	sendDataIn(conn, itt, cmdSN, statSN, resp, 0x00)
}

// handleModeSelect6 processes a MODE SELECT(6) command.
// Extracts the block length from the block descriptor and stores it.
func (m *MockTapeDrive) handleModeSelect6(conn net.Conn, itt, cmdSN uint32, statSN *uint32, data []byte) {
	if len(data) < 12 {
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, nil) // CHECK CONDITION
		return
	}

	bdLen := data[3]
	if bdLen < 8 {
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, nil)
		return
	}

	blockLength := int(data[9])<<16 | int(data[10])<<8 | int(data[11])

	m.mu.Lock()
	m.blockSize = blockLength
	m.mu.Unlock()

	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil) // GOOD
}

// makeFixedSense builds an 18-byte fixed-format sense data block.
// Response code is 0x70 (current errors, fixed format).
// Byte 2 encodes sense key (bits 3-0), filemark (bit 7), EOM (bit 6), ILI (bit 5).
// ASC is at byte 12, ASCQ at byte 13.
func makeFixedSense(senseKey uint8, filemark, eom, ili bool, asc, ascq uint8) []byte {
	sense := make([]byte, 18)
	sense[0] = 0x70 // Response code: current errors, fixed format
	sense[2] = senseKey & 0x0F
	if filemark {
		sense[2] |= 0x80
	}
	if eom {
		sense[2] |= 0x40
	}
	if ili {
		sense[2] |= 0x20
	}
	sense[7] = 10   // Additional sense length (18-8)
	sense[12] = asc
	sense[13] = ascq
	return sense
}

// makeFixedSenseWithInfo builds an 18-byte fixed-format sense data block
// with the VALID bit set and the INFORMATION field populated.
// Bytes 3-6 contain the 32-bit big-endian information value.
func makeFixedSenseWithInfo(senseKey uint8, filemark, eom, ili bool, asc, ascq uint8, information uint32) []byte {
	sense := makeFixedSense(senseKey, filemark, eom, ili, asc, ascq)
	sense[0] |= 0x80 // Set VALID bit
	binary.BigEndian.PutUint32(sense[3:7], information)
	return sense
}

// handleSPACE processes a SPACE(6) command (opcode 0x11).
// Supports space codes: 0=blocks, 1=filemarks, 2=sequential filemarks,
// 3=end-of-data. Setmarks (4, 5) return ILLEGAL REQUEST.
func (m *MockTapeDrive) handleSPACE(conn net.Conn, itt, cmdSN uint32, statSN *uint32, cdb [16]byte) {
	code := cdb[1] & 0x07
	// 24-bit signed count from bytes 2-4
	raw := uint32(cdb[2])<<16 | uint32(cdb[3])<<8 | uint32(cdb[4])
	count := int32(raw)
	if raw&0x800000 != 0 { // sign extend from 24-bit
		count = int32(raw | 0xFF000000)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	switch code {
	case 0x00: // Blocks
		m.spaceBlocks(conn, itt, cmdSN, statSN, count)
	case 0x01: // Filemarks
		m.spaceFilemarks(conn, itt, cmdSN, statSN, count)
	case 0x02: // Sequential filemarks (same as filemarks for mock)
		m.spaceFilemarks(conn, itt, cmdSN, statSN, count)
	case 0x03: // End-of-data
		m.position = m.written
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
	default: // Setmarks (0x04, 0x05) -- not supported
		sense := makeFixedSense(0x05, false, false, false, 0x20, 0x00) // ILLEGAL REQUEST / INVALID COMMAND OPERATION CODE
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
	}
}

// spaceBlocks advances or retreats by count blocks. Must be called with m.mu held.
func (m *MockTapeDrive) spaceBlocks(conn net.Conn, itt, cmdSN uint32, statSN *uint32, count int32) {
	step := m.blockSize
	if step == 0 {
		step = 1 // variable mode: each byte is a position unit
	}

	newPos := m.position + int(count)*step

	if newPos < 0 {
		m.position = 0
		sense := makeFixedSense(0x00, false, false, false, 0x00, 0x04) // NO SENSE / BEGINNING OF PARTITION/MEDIUM DETECTED
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	if count > 0 && newPos >= m.written {
		m.position = m.written
		sense := makeFixedSense(0x08, false, false, false, 0x00, 0x05) // BLANK CHECK / END-OF-DATA DETECTED
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
		return
	}

	m.position = newPos
	sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
}

// spaceFilemarks advances or retreats by count filemarks. Must be called with m.mu held.
func (m *MockTapeDrive) spaceFilemarks(conn net.Conn, itt, cmdSN uint32, statSN *uint32, count int32) {
	if count == 0 {
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
		return
	}

	sorted := make([]int, len(m.filemarks))
	copy(sorted, m.filemarks)
	sort.Ints(sorted)

	if count > 0 {
		// Forward: find the count-th filemark at or after current position
		found := 0
		for _, fmPos := range sorted {
			if fmPos >= m.position {
				found++
				if found == int(count) {
					m.position = fmPos // position at filemark (past it logically)
					sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
					return
				}
			}
		}
		// Not enough filemarks -- position at end-of-data, return BLANK CHECK
		m.position = m.written
		sense := makeFixedSense(0x08, false, false, false, 0x00, 0x05) // BLANK CHECK / END-OF-DATA
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
	} else {
		// Backward: find the abs(count)-th filemark before current position
		absCount := int(-count)
		found := 0
		for i := len(sorted) - 1; i >= 0; i-- {
			if sorted[i] < m.position {
				found++
				if found == absCount {
					m.position = sorted[i]
					sendSCSIResponse(conn, itt, cmdSN, statSN, 0x00, nil)
					return
				}
			}
		}
		// Not enough filemarks backward -- clamp to BOT
		m.position = 0
		sense := makeFixedSense(0x00, false, false, false, 0x00, 0x04) // BEGINNING OF PARTITION
		sendSCSIResponse(conn, itt, cmdSN, statSN, 0x02, sense)
	}
}

// --- iSCSI PDU framing helpers ---

// readPDU reads a complete iSCSI PDU (BHS + data segment) from the connection.
func readPDU(conn net.Conn) ([48]byte, []byte, error) {
	var bhs [48]byte
	if _, err := io.ReadFull(conn, bhs[:]); err != nil {
		return bhs, nil, err
	}

	// Data segment length: bytes 5-7 (3 bytes, big-endian)
	dsLen := uint32(bhs[5])<<16 | uint32(bhs[6])<<8 | uint32(bhs[7])

	var data []byte
	if dsLen > 0 {
		// Read data + padding (4-byte aligned)
		padded := dsLen
		if padded%4 != 0 {
			padded += 4 - (padded % 4)
		}
		buf := make([]byte, padded)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return bhs, nil, err
		}
		data = buf[:dsLen]
	}

	return bhs, data, nil
}

// writePDU writes a BHS and optional data segment to the connection.
func writePDU(conn net.Conn, bhs [48]byte, data []byte) error {
	if _, err := conn.Write(bhs[:]); err != nil {
		return err
	}
	if len(data) > 0 {
		if _, err := conn.Write(data); err != nil {
			return err
		}
		// Pad to 4-byte boundary
		pad := (4 - (len(data) % 4)) % 4
		if pad > 0 {
			if _, err := conn.Write(make([]byte, pad)); err != nil {
				return err
			}
		}
	}
	return nil
}

// sendSCSIResponse sends a SCSI Response PDU with no data.
func sendSCSIResponse(conn net.Conn, itt, cmdSN uint32, statSN *uint32, status byte, senseData []byte) {
	sn := *statSN
	*statSN++

	var dataSegment []byte
	if len(senseData) > 0 {
		// Per RFC 7143 Section 11.4.7.2: [SenseLength (2 bytes)] [Sense Data]
		dataSegment = make([]byte, 2+len(senseData))
		binary.BigEndian.PutUint16(dataSegment[0:2], uint16(len(senseData)))
		copy(dataSegment[2:], senseData)
	}

	var resp [48]byte
	resp[0] = 0x21 // SCSI Response opcode
	resp[1] = 0x80 // Final
	resp[2] = 0x00 // Response: command completed at target
	resp[3] = status
	// Data segment length
	dsLen := uint32(len(dataSegment))
	resp[5] = byte(dsLen >> 16)
	resp[6] = byte(dsLen >> 8)
	resp[7] = byte(dsLen)
	binary.BigEndian.PutUint32(resp[16:20], itt)
	binary.BigEndian.PutUint32(resp[24:28], sn)       // StatSN
	binary.BigEndian.PutUint32(resp[28:32], cmdSN+1)  // ExpCmdSN
	binary.BigEndian.PutUint32(resp[32:36], cmdSN+10) // MaxCmdSN

	writePDU(conn, resp, dataSegment)
}

// sendDataIn sends a Data-In PDU with F+S bits set (final data with status).
func sendDataIn(conn net.Conn, itt, cmdSN uint32, statSN *uint32, data []byte, status byte) {
	sn := *statSN
	*statSN++

	var resp [48]byte
	resp[0] = 0x25 // Data-In opcode
	resp[1] = 0x80 | 0x01 // F-bit (final) | S-bit (status)
	resp[3] = status
	// Data segment length
	dsLen := uint32(len(data))
	resp[5] = byte(dsLen >> 16)
	resp[6] = byte(dsLen >> 8)
	resp[7] = byte(dsLen)
	binary.BigEndian.PutUint32(resp[16:20], itt)
	binary.BigEndian.PutUint32(resp[24:28], sn)       // StatSN
	binary.BigEndian.PutUint32(resp[28:32], cmdSN+1)  // ExpCmdSN
	binary.BigEndian.PutUint32(resp[32:36], cmdSN+10) // MaxCmdSN
	// DataSN = 0
	binary.BigEndian.PutUint32(resp[36:40], 0)

	writePDU(conn, resp, data)
}

// makeLoginResp builds a Login Response BHS.
func makeLoginResp(isid [6]byte, itt, cmdSN uint32, csg, nsg uint8, transit bool, data []byte, tsih uint16) [48]byte {
	var bhs [48]byte
	bhs[0] = 0x23 // Login Response opcode

	flags := (csg & 0x03) << 2
	flags |= nsg & 0x03
	if transit {
		flags |= 0x80
	}
	bhs[1] = flags

	// Data segment length
	dsLen := uint32(len(data))
	bhs[5] = byte(dsLen >> 16)
	bhs[6] = byte(dsLen >> 8)
	bhs[7] = byte(dsLen)

	// ISID (bytes 8-13)
	copy(bhs[8:14], isid[:])
	// TSIH (bytes 14-15)
	binary.BigEndian.PutUint16(bhs[14:16], tsih)
	// ITT
	binary.BigEndian.PutUint32(bhs[16:20], itt)
	// StatSN (byte 24-27)
	binary.BigEndian.PutUint32(bhs[24:28], 1)
	// ExpCmdSN
	binary.BigEndian.PutUint32(bhs[28:32], cmdSN)
	// MaxCmdSN
	binary.BigEndian.PutUint32(bhs[32:36], cmdSN+10)

	return bhs
}

// --- Text key-value helpers ---

// parseTextKV parses iSCSI text key=value pairs from a data segment.
func parseTextKV(data []byte) map[string]string {
	result := make(map[string]string)
	if len(data) == 0 {
		return result
	}
	s := string(data)
	for _, pair := range strings.Split(s, "\x00") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

// encodeTextKV encodes key=value pairs into an iSCSI text data segment.
func encodeTextKV(kvs map[string]string) []byte {
	var buf []byte
	for k, v := range kvs {
		buf = append(buf, []byte(k+"="+v+"\x00")...)
	}
	return buf
}

// padString right-pads s to length with spaces and returns bytes.
func padString(s string, length int) []byte {
	b := make([]byte, length)
	for i := range b {
		b[i] = ' '
	}
	copy(b, s)
	return b
}
