package kminv

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(db.index) != 0 {
		t.Fatalf("index length = %d, want 0", len(db.index))
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() != logHeaderSize {
		t.Fatalf("file size = %d, want %d", info.Size(), logHeaderSize)
	}
}

func TestOpenRecoversMultipleSetRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	records := []Record{
		{Op: OpSet, Key: []byte("one"), Value: []byte("1")},
		{Op: OpSet, Key: []byte("two"), Value: []byte("2")},
	}
	offsets := writeRecoveryRecords(t, path, records)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if got := db.index["one"]; got != offsets[0] {
		t.Fatalf("index[one] = %d, want %d", got, offsets[0])
	}
	if got := db.index["two"]; got != offsets[1] {
		t.Fatalf("index[two] = %d, want %d", got, offsets[1])
	}
}

func TestOpenRecoveryLatestSetWins(t *testing.T) {
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

	if got := db.index["key"]; got != offsets[1] {
		t.Fatalf("index[key] = %d, want latest offset %d", got, offsets[1])
	}
}

func TestOpenRecoveryDeleteRemovesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	writeRecoveryRecords(t, path, []Record{
		{Op: OpSet, Key: []byte("key"), Value: []byte("value")},
		{Op: OpDelete, Key: []byte("key")},
	})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if _, ok := db.index["key"]; ok {
		t.Fatal("deleted key is present in index")
	}
}

func TestOpenRecoveryTruncatesIncompleteLengthPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	offsets := writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key"), Value: []byte("value")}})
	appendRecoveryBytes(t, path, []byte{0x00, 0x00, 0x00})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertFileSize(t, path, offsets[0]+int64(len(record)))
	assertRecoveryRecordReadable(t, path, offsets[0], record)
}

func TestOpenRecoveryTruncatesIncompleteRecordPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	offsets := writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key"), Value: []byte("value")}})

	tail := make([]byte, 4+5)
	binary.BigEndian.PutUint32(tail[:4], 13)
	copy(tail[4:], []byte{0x01, 0x00, 0x00, 0x00, 0x00})
	appendRecoveryBytes(t, path, tail)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertFileSize(t, path, offsets[0]+int64(len(record)))
	assertRecoveryRecordReadable(t, path, offsets[0], record)
}

func TestOpenRejectsInvalidChecksumDuringRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	record[4] ^= 0xff
	createRecoveryLog(t, path)
	appendRecoveryBytes(t, path, record)

	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("Open() error = nil, want checksum error")
	}
}

func TestOpenRejectsInvalidOperationDuringRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	record[8] = 0xff
	binary.BigEndian.PutUint32(record[4:8], crc32.ChecksumIEEE(record[8:]))
	createRecoveryLog(t, path)
	appendRecoveryBytes(t, path, record)

	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("Open() error = nil, want invalid operation error")
	}
}

func TestOpenRejectsImpossibleRecordLengthDuringRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := make([]byte, 4)
	binary.BigEndian.PutUint32(record, 12)
	createRecoveryLog(t, path)
	appendRecoveryBytes(t, path, record)

	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("Open() error = nil, want invalid length error")
	}
}

func TestValidateLogReturnsRecordBoundariesAndEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	offsets := writeRecoveryRecords(t, path, []Record{
		{Op: OpSet, Key: []byte("one"), Value: []byte("1")},
		{Op: OpSet, Key: []byte("two"), Value: []byte("2")},
	})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	validation, err := db.validateLog()
	if err != nil {
		t.Fatalf("validateLog() error = %v", err)
	}
	wantEnd := offsets[1] + int64(len(encodedRecoveryRecord(t, Record{
		Op: OpSet, Key: []byte("two"), Value: []byte("2"),
	})))
	if validation.end != wantEnd {
		t.Fatalf("validation end = %d, want %d", validation.end, wantEnd)
	}
	for _, offset := range []int64{logHeaderSize, offsets[1], wantEnd} {
		if !validation.hasBoundary(offset) {
			t.Errorf("boundary %d is missing", offset)
		}
	}
	for _, offset := range []int64{logHeaderSize + 1, offsets[1] + 1} {
		if validation.hasBoundary(offset) {
			t.Errorf("offset %d inside a record was reported as a boundary", offset)
		}
	}
}

func TestValidateLogTruncatesOnlyIncompleteFinalTails(t *testing.T) {
	tests := []struct {
		name string
		tail []byte
	}{
		{name: "length prefix", tail: []byte{0x00, 0x00, 0x00}},
		{
			name: "record body",
			tail: func() []byte {
				tail := make([]byte, 4+5)
				binary.BigEndian.PutUint32(tail[:4], 13)
				copy(tail[4:], []byte{OpSet, 0, 0, 0, 0})
				return tail
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.log")
			record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
			offsets := writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key"), Value: []byte("value")}})
			appendRecoveryBytes(t, path, test.tail)

			log, err := openLog(path)
			if err != nil {
				t.Fatalf("openLog() error = %v", err)
			}
			db := &DB{log: log}
			validation, err := db.validateLog()
			if err != nil {
				_ = log.close()
				t.Fatalf("validateLog() error = %v", err)
			}
			if err := log.close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			wantEnd := offsets[0] + int64(len(record))
			if validation.end != wantEnd {
				t.Fatalf("validation end = %d, want %d", validation.end, wantEnd)
			}
			assertFileSize(t, path, wantEnd)
		})
	}
}

func TestOpenDoesNotSkipCompleteCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	record := encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	record[4] ^= 0xff
	createRecoveryLog(t, path)
	appendRecoveryBytes(t, path, record)
	appendRecoveryBytes(t, path, encodedRecoveryRecord(t, Record{Op: OpSet, Key: []byte("other"), Value: []byte("value")}))

	if db, err := Open(path); err == nil {
		_ = db.Close()
		t.Fatal("Open() error = nil, want corruption error")
	}
}

func writeRecoveryRecords(t *testing.T, path string, records []Record) []int64 {
	t.Helper()

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	offsets := make([]int64, 0, len(records))
	for _, record := range records {
		data := encodedRecoveryRecord(t, record)
		offset, err := log.appendRecord(data)
		if err != nil {
			t.Fatalf("appendRecord() error = %v", err)
		}
		offsets = append(offsets, offset)
	}
	return offsets
}

func encodedRecoveryRecord(t *testing.T, record Record) []byte {
	t.Helper()
	data, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	return data
}

func createRecoveryLog(t *testing.T, path string) {
	t.Helper()

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	if err := log.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
}

func appendRecoveryBytes(t *testing.T, path string, data []byte) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func assertFileSize(t *testing.T, path string, want int64) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() != want {
		t.Fatalf("file size = %d, want %d", info.Size(), want)
	}
}

func assertRecoveryRecordReadable(t *testing.T, path string, offset int64, want []byte) {
	t.Helper()

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	got, err := log.readRecord(offset)
	if err != nil {
		t.Fatalf("readRecord() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("record = %v, want %v", got, want)
	}
}
