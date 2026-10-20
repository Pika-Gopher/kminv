package kminv

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	OpSet           byte   = 0x01
	OpDelete        byte   = 0x02
	maxKeyLength    uint32 = 1024
	maxRecordLength uint32 = 1049613
)

type Record struct {
	Op    byte
	Key   []byte
	Value []byte
}

func encodeRecord(r Record) ([]byte, error) {
	switch r.Op {
	case OpSet, OpDelete:
	default:
		return nil, fmt.Errorf("invalid record operation: %d", r.Op)
	}

	if len(r.Key) > int(maxKeyLength) {
		return nil, fmt.Errorf("key is too large: %d bytes", len(r.Key))
	}
	if len(r.Value) > 1<<20 {
		return nil, fmt.Errorf("value is too large: %d bytes", len(r.Value))
	}
	if r.Op == OpDelete && len(r.Value) != 0 {
		return nil, fmt.Errorf("delete record must have an empty value")
	}

	// The 17-byte header includes the length prefix and checksum.
	physicalSize := 17 + len(r.Key) + len(r.Value)
	buf := make([]byte, physicalSize)

	// The length prefix excludes its own four bytes.
	binary.BigEndian.PutUint32(buf[0:4], uint32(physicalSize-4))
	buf[8] = r.Op
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(r.Key)))
	binary.BigEndian.PutUint32(buf[13:17], uint32(len(r.Value)))

	keyEnd := 17 + len(r.Key)
	copy(buf[17:keyEnd], r.Key)
	copy(buf[keyEnd:], r.Value)

	// The checksum covers the operation, lengths, key, and value.
	binary.BigEndian.PutUint32(buf[4:8], crc32.ChecksumIEEE(buf[8:]))
	return buf, nil
}

func decodeRecord(data []byte) (Record, error) {
	if len(data) < 17 {
		return Record{}, fmt.Errorf("record is too short: %d bytes", len(data))
	}

	recordLength := binary.BigEndian.Uint32(data[0:4])
	if uint64(recordLength) != uint64(len(data)-4) {
		return Record{}, fmt.Errorf("record length mismatch: declared %d, got %d", recordLength, len(data)-4)
	}
	if recordLength > maxRecordLength {
		return Record{}, fmt.Errorf("record is too large: %d bytes", recordLength)
	}

	op := data[8]
	keyLength := binary.BigEndian.Uint32(data[9:13])
	valueLength := binary.BigEndian.Uint32(data[13:17])

	switch op {
	case OpSet, OpDelete:
	default:
		return Record{}, fmt.Errorf("invalid record operation: %d", op)
	}
	if keyLength > maxKeyLength {
		return Record{}, fmt.Errorf("key is too large: %d bytes", keyLength)
	}
	if valueLength > 1<<20 {
		return Record{}, fmt.Errorf("value is too large: %d bytes", valueLength)
	}
	if op == OpDelete && valueLength != 0 {
		return Record{}, fmt.Errorf("delete record must have an empty value")
	}

	if uint64(keyLength)+uint64(valueLength) != uint64(len(data)-17) {
		return Record{}, fmt.Errorf("record payload length mismatch")
	}

	if binary.BigEndian.Uint32(data[4:8]) != crc32.ChecksumIEEE(data[8:]) {
		return Record{}, fmt.Errorf("record checksum mismatch")
	}

	keyEnd := 17 + int(keyLength)
	return Record{
		Op:    op,
		Key:   append([]byte(nil), data[17:keyEnd]...),
		Value: append([]byte(nil), data[keyEnd:]...),
	}, nil
}
