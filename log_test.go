package kminv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogCreatesHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")

	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	if err := log.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data[:8]) != logMagic {
		t.Fatalf("magic = %q, want %q", data[:8], logMagic)
	}
	if version := binary.BigEndian.Uint32(data[8:]); version != logVersion {
		t.Fatalf("version = %d, want %d", version, logVersion)
	}
}

func TestOpenLogReopensValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")

	first, err := openLog(path)
	if err != nil {
		t.Fatalf("first openLog() error = %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("first close() error = %v", err)
	}

	second, err := openLog(path)
	if err != nil {
		t.Fatalf("second openLog() error = %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatalf("second close() error = %v", err)
	}
}

func TestOpenLogRejectsTruncatedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	if err := os.WriteFile(path, []byte(logMagic[:4]), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := openLog(path)
	if err == nil || !strings.Contains(err.Error(), "truncated header") {
		t.Fatalf("openLog() error = %v, want truncated header error", err)
	}
}

func TestOpenLogRejectsInvalidMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	header := make([]byte, logHeaderSize)
	copy(header[:8], "INVALID!")
	binary.BigEndian.PutUint32(header[8:], logVersion)
	if err := os.WriteFile(path, header, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := openLog(path)
	if err == nil || !strings.Contains(err.Error(), "invalid magic") {
		t.Fatalf("openLog() error = %v, want invalid magic error", err)
	}
}

func TestOpenLogRejectsUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	header := make([]byte, logHeaderSize)
	copy(header[:8], logMagic)
	binary.BigEndian.PutUint32(header[8:], logVersion+1)
	if err := os.WriteFile(path, header, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := openLog(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("openLog() error = %v, want unsupported version error", err)
	}
}

func TestLogFileAppendRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}

	first := []byte("first record")
	firstOffset, err := log.appendRecord(first)
	if err != nil {
		t.Fatalf("appendRecord(first) error = %v", err)
	}
	if firstOffset != logHeaderSize {
		t.Fatalf("first offset = %d, want %d", firstOffset, logHeaderSize)
	}

	second := []byte("second record")
	secondOffset, err := log.appendRecord(second)
	if err != nil {
		t.Fatalf("appendRecord(second) error = %v", err)
	}
	if secondOffset != firstOffset+int64(len(first)) {
		t.Fatalf("second offset = %d, want %d", secondOffset, firstOffset+int64(len(first)))
	}

	if err := log.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := append(append([]byte{}, data[:logHeaderSize]...), append(first, second...)...)
	if !bytes.Equal(data, want) {
		t.Fatalf("file contents = %q, want header followed by %q", data, append(first, second...))
	}
}

func TestLogFileReadRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}

	first := encodedLogTestRecord(t, Record{Op: OpSet, Key: []byte("first"), Value: []byte("value-1")})
	firstOffset, err := log.appendRecord(first)
	if err != nil {
		t.Fatalf("appendRecord(first) error = %v", err)
	}
	second := encodedLogTestRecord(t, Record{Op: OpDelete, Key: []byte("second")})
	secondOffset, err := log.appendRecord(second)
	if err != nil {
		t.Fatalf("appendRecord(second) error = %v", err)
	}
	defer log.close()

	gotFirst, err := log.readRecord(firstOffset)
	if err != nil {
		t.Fatalf("readRecord(first) error = %v", err)
	}
	if !bytes.Equal(gotFirst, first) {
		t.Fatalf("first record = %v, want %v", gotFirst, first)
	}

	gotSecond, err := log.readRecord(secondOffset)
	if err != nil {
		t.Fatalf("readRecord(second) error = %v", err)
	}
	if !bytes.Equal(gotSecond, second) {
		t.Fatalf("second record = %v, want %v", gotSecond, second)
	}
}

func TestLogFileReadRecordRejectsInvalidOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	record := encodedLogTestRecord(t, Record{Op: OpSet, Key: []byte("key")})
	offset, err := log.appendRecord(record)
	if err != nil {
		t.Fatalf("appendRecord() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		offset int64
	}{
		{name: "inside header", offset: logHeaderSize - 1},
		{name: "outside file", offset: offset + int64(len(record))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := log.readRecord(test.offset); err == nil {
				t.Fatalf("readRecord(%d) error = nil, want error", test.offset)
			}
		})
	}
}

func TestLogFileReadRecordRejectsTruncatedLengthPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	offset, err := log.appendRecord([]byte{0x00, 0x00})
	if err != nil {
		t.Fatalf("appendRecord() error = %v", err)
	}
	if _, err := log.readRecord(offset); err == nil || !strings.Contains(err.Error(), "truncated length prefix") {
		t.Fatalf("readRecord() error = %v, want truncated length prefix error", err)
	}
}

func TestLogFileReadRecordRejectsTruncatedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	data := make([]byte, 16)
	binary.BigEndian.PutUint32(data, 13)
	offset, err := log.appendRecord(data)
	if err != nil {
		t.Fatalf("appendRecord() error = %v", err)
	}
	if _, err := log.readRecord(offset); err == nil || !strings.Contains(err.Error(), "truncated record") {
		t.Fatalf("readRecord() error = %v, want truncated record error", err)
	}
}

func TestLogFileReadRecordRejectsImpossibleLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, 12)
	offset, err := log.appendRecord(data)
	if err != nil {
		t.Fatalf("appendRecord() error = %v", err)
	}
	if _, err := log.readRecord(offset); err == nil || !strings.Contains(err.Error(), "invalid record length") {
		t.Fatalf("readRecord() error = %v, want invalid record length error", err)
	}
}

func encodedLogTestRecord(t *testing.T, record Record) []byte {
	t.Helper()
	data, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	return data
}

func TestLogFileSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	if err := log.sync(); err != nil {
		t.Fatalf("sync() error = %v", err)
	}
}

func TestLogFileTruncateToHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	record := encodedLogTestRecord(t, Record{Op: OpSet, Key: []byte("key"), Value: []byte("value")})
	if _, err := log.appendRecord(record); err != nil {
		t.Fatalf("appendRecord() error = %v", err)
	}
	if err := log.truncate(logHeaderSize); err != nil {
		t.Fatalf("truncate() error = %v", err)
	}

	info, err := log.file.Stat()
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() != logHeaderSize {
		t.Fatalf("file size = %d, want %d", info.Size(), logHeaderSize)
	}
}

func TestLogFileTruncateRejectsInvalidSizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}
	defer log.close()

	for _, size := range []int64{-1, logHeaderSize - 1} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			if err := log.truncate(size); err == nil {
				t.Fatalf("truncate(%d) error = nil, want error", size)
			}
		})
	}
}
