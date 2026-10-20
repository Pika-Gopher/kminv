package kminv

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"path/filepath"
	"testing"
)

func TestSnapshotRoundTrip(t *testing.T) {
	want := snapshot{
		logPosition: 100,
		index: map[string]int64{
			"beta":  60,
			"alpha": 28,
		},
	}
	data, err := encodeSnapshot(want)
	if err != nil {
		t.Fatalf("encodeSnapshot() error = %v", err)
	}
	got, err := decodeSnapshot(data)
	if err != nil {
		t.Fatalf("decodeSnapshot() error = %v", err)
	}
	if got.logPosition != want.logPosition || !snapshotIndexesEqual(got.index, want.index) {
		t.Fatalf("decoded snapshot = %#v, want %#v", got, want)
	}
}

func TestSnapshotEmptyAndEmptyKey(t *testing.T) {
	for _, want := range []snapshot{
		{logPosition: logHeaderSize, index: map[string]int64{}},
		{logPosition: 28, index: map[string]int64{"": 12}},
	} {
		data, err := encodeSnapshot(want)
		if err != nil {
			t.Fatalf("encodeSnapshot() error = %v", err)
		}
		got, err := decodeSnapshot(data)
		if err != nil {
			t.Fatalf("decodeSnapshot() error = %v", err)
		}
		if !snapshotIndexesEqual(got.index, want.index) {
			t.Fatalf("decoded index = %#v, want %#v", got.index, want.index)
		}
	}
}

func TestSnapshotBinaryKeys(t *testing.T) {
	key := string([]byte{0x00, 0xff, 0x00})
	want := snapshot{logPosition: 40, index: map[string]int64{key: 28}}
	data, err := encodeSnapshot(want)
	if err != nil {
		t.Fatalf("encodeSnapshot() error = %v", err)
	}
	got, err := decodeSnapshot(data)
	if err != nil {
		t.Fatalf("decodeSnapshot() error = %v", err)
	}
	if !snapshotIndexesEqual(got.index, want.index) {
		t.Fatalf("decoded index = %#v, want %#v", got.index, want.index)
	}
}

func TestSnapshotSerializationIsDeterministic(t *testing.T) {
	first, err := encodeSnapshot(snapshot{logPosition: 100, index: map[string]int64{"z": 60, "a": 28}})
	if err != nil {
		t.Fatalf("first encodeSnapshot() error = %v", err)
	}
	second, err := encodeSnapshot(snapshot{logPosition: 100, index: map[string]int64{"a": 28, "z": 60}})
	if err != nil {
		t.Fatalf("second encodeSnapshot() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("snapshot encodings differ: %v and %v", first, second)
	}
}

func TestSnapshotDecodeRejectsMalformedData(t *testing.T) {
	valid := encodedTestSnapshot(t)
	tests := []struct {
		name string
		data func() []byte
	}{
		{"start magic", func() []byte { data := cloneBytes(valid); data[0] ^= 1; return data }},
		{"end magic", func() []byte { data := cloneBytes(valid); data[len(data)-1] ^= 1; return data }},
		{"version", func() []byte { data := cloneBytes(valid); binary.BigEndian.PutUint32(data[8:12], 2); return data }},
		{"truncated header", func() []byte { return valid[:10] }},
		{"truncated payload", func() []byte { return valid[:len(valid)-13] }},
		{"truncated checksum", func() []byte { return valid[:len(valid)-9] }},
		{"truncated trailer", func() []byte { return valid[:len(valid)-4] }},
		{"payload length", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[12:20], 1)
			return data
		}},
		{"entry count", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint32(data[28:32], 2)
			refreshSnapshotChecksum(data)
			return data
		}},
		{"key length", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint32(data[32:36], maxKeyLength+1)
			refreshSnapshotChecksum(data)
			return data
		}},
		{"checksum", func() []byte { data := cloneBytes(valid); data[36] ^= 1; return data }},
		{"log position", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[20:28], uint64(logHeaderSize-1))
			refreshSnapshotChecksum(data)
			return data
		}},
		{"offset", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[snapshotEntryOffset(3):snapshotEntryOffset(3)+8], uint64(logHeaderSize-1))
			refreshSnapshotChecksum(data)
			return data
		}},
		{"offset equals log position", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[snapshotEntryOffset(3):snapshotEntryOffset(3)+8], 100)
			refreshSnapshotChecksum(data)
			return data
		}},
		{"offset exceeds max int64", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[snapshotEntryOffset(3):snapshotEntryOffset(3)+8], math.MaxUint64)
			refreshSnapshotChecksum(data)
			return data
		}},
		{"overflow length", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[12:20], math.MaxUint64)
			return data
		}},
		{"payload too short for entry count", func() []byte {
			data := cloneBytes(valid)
			binary.BigEndian.PutUint64(data[12:20], 3)
			return data
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeSnapshot(test.data()); err == nil {
				t.Fatal("decodeSnapshot() error = nil, want error")
			}
		})
	}
}

func TestSnapshotRejectsDuplicateKeys(t *testing.T) {
	duplicate := make([]byte, 4+2*(4+3+8))
	binary.BigEndian.PutUint32(duplicate[:4], 2)
	cursor := 4
	for i := 0; i < 2; i++ {
		binary.BigEndian.PutUint32(duplicate[cursor:cursor+4], 3)
		cursor += 4
		copy(duplicate[cursor:cursor+3], []byte("key"))
		cursor += 3
		binary.BigEndian.PutUint64(duplicate[cursor:cursor+8], 28)
		cursor += 8
	}
	data := make([]byte, snapshotHeaderSize+len(duplicate)+snapshotTrailerSize)
	copy(data[:8], snapshotStartMagic[:])
	binary.BigEndian.PutUint32(data[8:12], snapshotVersion)
	binary.BigEndian.PutUint64(data[12:20], uint64(len(duplicate)))
	binary.BigEndian.PutUint64(data[20:28], 100)
	copy(data[28:], duplicate)
	copy(data[len(data)-8:], snapshotEndMagic[:])
	refreshSnapshotChecksum(data)
	if _, err := decodeSnapshot(data); err == nil {
		t.Fatal("decodeSnapshot() error = nil, want duplicate-key error")
	}
}

func TestSnapshotEncodeRejectsInvalidMetadata(t *testing.T) {
	tests := []snapshot{
		{logPosition: logHeaderSize - 1},
		{logPosition: 20, index: map[string]int64{"key": 20}},
		{logPosition: 100, index: map[string]int64{string(make([]byte, int(maxKeyLength)+1)): 28}},
	}
	for _, test := range tests {
		if _, err := encodeSnapshot(test); err == nil {
			t.Fatalf("encodeSnapshot(%#v) error = nil, want error", test)
		}
	}
}

func TestValidateSnapshotAcceptsMatchingPrefixAndSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	records := []Record{
		{Op: OpSet, Key: []byte("key"), Value: []byte("old")},
		{Op: OpSet, Key: []byte("key"), Value: []byte("new")},
	}
	offsets := writeRecoveryRecords(t, path, records)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	prefixEnd := offsets[0] + int64(len(encodedRecoveryRecord(t, records[0])))
	s := snapshot{logPosition: prefixEnd, index: map[string]int64{"key": offsets[0]}}
	if err := db.validateSnapshot(s); err != nil {
		t.Fatalf("validateSnapshot() error = %v", err)
	}
}

func TestValidateSnapshotAcceptsEmptyAndBinaryKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	records := []Record{
		{Op: OpSet, Key: []byte{}, Value: []byte{}},
		{Op: OpSet, Key: []byte{0x00, 0xff, 0x01}, Value: []byte{0x00, 0xfe}},
	}
	offsets := writeRecoveryRecords(t, path, records)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	end := offsets[1] + int64(len(encodedRecoveryRecord(t, records[1])))
	s := snapshot{logPosition: end, index: map[string]int64{
		"":                               offsets[0],
		string([]byte{0x00, 0xff, 0x01}): offsets[1],
	}}
	if err := db.validateSnapshot(s); err != nil {
		t.Fatalf("validateSnapshot() error = %v", err)
	}
}

func TestValidateSnapshotRejectsInvalidPositionsAndOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")}
	offsets := writeRecoveryRecords(t, path, []Record{record})
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	end := offsets[0] + int64(len(encodedRecoveryRecord(t, record)))
	tests := []snapshot{
		{logPosition: logHeaderSize + 1},
		{logPosition: end + 1},
		{logPosition: end, index: map[string]int64{"key": offsets[0] + 1}},
		{logPosition: end, index: map[string]int64{"key": end}},
		{logPosition: offsets[0], index: map[string]int64{"key": offsets[0]}},
	}
	for i, test := range tests {
		if err := db.validateSnapshot(test); err == nil {
			t.Errorf("case %d: validateSnapshot() error = nil, want error", i)
		}
	}
}

func TestValidateSnapshotRejectsWrongRecordAndSemanticMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	records := []Record{
		{Op: OpSet, Key: []byte("key"), Value: []byte("old")},
		{Op: OpSet, Key: []byte("other"), Value: []byte("value")},
		{Op: OpDelete, Key: []byte("key")},
	}
	offsets := writeRecoveryRecords(t, path, records)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	end := offsets[2] + int64(len(encodedRecoveryRecord(t, records[2])))

	tests := []snapshot{
		{logPosition: end, index: map[string]int64{"key": offsets[2]}},
		{logPosition: end, index: map[string]int64{"key": offsets[1]}},
		{logPosition: end, index: map[string]int64{"key": offsets[0]}},
		{logPosition: end, index: map[string]int64{"key": offsets[0], "other": offsets[1]}},
	}
	for i, test := range tests {
		if err := db.validateSnapshot(test); err == nil {
			t.Errorf("case %d: validateSnapshot() error = nil, want error", i)
		}
	}
}

func TestValidateSnapshotReportsAuthoritativeLogCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	record[4] ^= 0xff
	createRecoveryLog(t, path)
	appendRecoveryBytes(t, path, record)

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()
	db := &DB{log: log}
	err = db.validateSnapshot(snapshot{logPosition: logHeaderSize, index: map[string]int64{}})
	if err == nil {
		t.Fatal("validateSnapshot() error = nil, want authoritative log corruption error")
	}
}

func TestValidateSnapshotDoesNotTruncateIncompleteTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")}
	offsets := writeRecoveryRecords(t, path, []Record{record})
	encoded := encodedRecoveryRecord(t, record)
	tail := []byte{0, 0, 0}
	appendRecoveryBytes(t, path, tail)
	before := offsets[0] + int64(len(encoded)) + int64(len(tail))

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	db := &DB{log: log}
	prefixEnd := offsets[0] + int64(len(encoded))
	err = db.validateSnapshot(snapshot{
		logPosition: prefixEnd,
		index:       map[string]int64{"key": offsets[0]},
	})
	if err == nil {
		_ = log.close()
		t.Fatal("validateSnapshot() error = nil, want incomplete-tail error")
	}
	if err := log.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	assertFileSize(t, path, before)
}

func encodedTestSnapshot(t *testing.T) []byte {
	t.Helper()
	data, err := encodeSnapshot(snapshot{logPosition: 100, index: map[string]int64{"key": 28}})
	if err != nil {
		t.Fatalf("encodeSnapshot() error = %v", err)
	}
	return data
}

func refreshSnapshotChecksum(data []byte) {
	payloadEnd := len(data) - snapshotTrailerSize
	binary.BigEndian.PutUint32(data[payloadEnd:payloadEnd+4], crc32.ChecksumIEEE(data[8:payloadEnd]))
}

func snapshotEntryOffset(keyLength int) int {
	return snapshotHeaderSize + 4 + 4 + keyLength
}

func cloneBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}

func snapshotIndexesEqual(left, right map[string]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, offset := range left {
		if right[key] != offset {
			return false
		}
	}
	return true
}
