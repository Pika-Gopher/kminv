package kminv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	snapshotVersion     uint32 = 1
	snapshotHeaderSize         = 28
	snapshotTrailerSize        = 12
)

var (
	snapshotStartMagic = [8]byte{'K', 'M', 'I', 'N', 'V', 'I', 'D', 'X'}
	snapshotEndMagic   = [8]byte{'K', 'M', 'I', 'N', 'V', 'E', 'N', 'D'}
)

type snapshot struct {
	logPosition int64
	index       map[string]int64
}

func encodeSnapshot(s snapshot) ([]byte, error) {
	if s.logPosition < logHeaderSize {
		return nil, fmt.Errorf("invalid snapshot log position: %d", s.logPosition)
	}
	if uint64(len(s.index)) > math.MaxUint32 {
		return nil, fmt.Errorf("too many snapshot entries: %d", len(s.index))
	}

	keys := make([]string, 0, len(s.index))
	for key, offset := range s.index {
		if len(key) > int(maxKeyLength) {
			return nil, fmt.Errorf("snapshot key is too large: %d bytes", len(key))
		}
		if offset < logHeaderSize || offset >= s.logPosition {
			return nil, fmt.Errorf("invalid snapshot offset %d for key", offset)
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare([]byte(keys[i]), []byte(keys[j])) < 0
	})

	payloadSize := uint64(4)
	for _, key := range keys {
		if len(key) > math.MaxUint32-12 {
			return nil, fmt.Errorf("snapshot key is too large: %d bytes", len(key))
		}
		payloadSize += uint64(12 + len(key))
		if payloadSize > uint64(math.MaxInt)-uint64(snapshotHeaderSize+snapshotTrailerSize) {
			return nil, fmt.Errorf("snapshot payload is too large")
		}
	}
	payload := make([]byte, int(payloadSize))
	binary.BigEndian.PutUint32(payload[:4], uint32(len(keys)))
	cursor := 4
	for _, key := range keys {
		binary.BigEndian.PutUint32(payload[cursor:cursor+4], uint32(len(key)))
		cursor += 4
		copy(payload[cursor:cursor+len(key)], key)
		cursor += len(key)
		binary.BigEndian.PutUint64(payload[cursor:cursor+8], uint64(s.index[key]))
		cursor += 8
	}

	data := make([]byte, snapshotHeaderSize+len(payload)+snapshotTrailerSize)
	copy(data[:8], snapshotStartMagic[:])
	binary.BigEndian.PutUint32(data[8:12], snapshotVersion)
	binary.BigEndian.PutUint64(data[12:20], uint64(len(payload)))
	binary.BigEndian.PutUint64(data[20:28], uint64(s.logPosition))
	copy(data[28:28+len(payload)], payload)

	checksumEnd := snapshotHeaderSize + len(payload)
	binary.BigEndian.PutUint32(data[checksumEnd:checksumEnd+4], crc32.ChecksumIEEE(data[8:checksumEnd]))
	copy(data[checksumEnd+4:], snapshotEndMagic[:])
	return data, nil
}

func decodeSnapshot(data []byte) (snapshot, error) {
	if len(data) < snapshotHeaderSize+snapshotTrailerSize {
		return snapshot{}, fmt.Errorf("snapshot is truncated")
	}
	if !bytes.Equal(data[:8], snapshotStartMagic[:]) {
		return snapshot{}, fmt.Errorf("invalid snapshot start magic")
	}
	if version := binary.BigEndian.Uint32(data[8:12]); version != snapshotVersion {
		return snapshot{}, fmt.Errorf("unsupported snapshot version %d", version)
	}

	payloadLength := binary.BigEndian.Uint64(data[12:20])
	availablePayload := len(data) - snapshotHeaderSize - snapshotTrailerSize
	if payloadLength > uint64(availablePayload) || payloadLength != uint64(availablePayload) {
		return snapshot{}, fmt.Errorf("snapshot payload length mismatch")
	}
	payloadEnd := snapshotHeaderSize + int(payloadLength)
	if !bytes.Equal(data[payloadEnd+4:], snapshotEndMagic[:]) {
		return snapshot{}, fmt.Errorf("invalid snapshot end magic")
	}
	if binary.BigEndian.Uint32(data[payloadEnd:payloadEnd+4]) != crc32.ChecksumIEEE(data[8:payloadEnd]) {
		return snapshot{}, fmt.Errorf("snapshot checksum mismatch")
	}

	logPosition := binary.BigEndian.Uint64(data[20:28])
	if logPosition < uint64(logHeaderSize) || logPosition > math.MaxInt64 {
		return snapshot{}, fmt.Errorf("invalid snapshot log position")
	}
	if payloadLength < 4 {
		return snapshot{}, fmt.Errorf("snapshot payload is too short")
	}

	payload := data[snapshotHeaderSize:payloadEnd]
	entryCount := binary.BigEndian.Uint32(payload[:4])
	if uint64(entryCount) > uint64((len(payload)-4)/12) {
		return snapshot{}, fmt.Errorf("snapshot entry count exceeds payload")
	}

	result := snapshot{
		logPosition: int64(logPosition),
		index:       make(map[string]int64, entryCount),
	}
	cursor := 4
	for i := uint32(0); i < entryCount; i++ {
		if len(payload)-cursor < 4 {
			return snapshot{}, fmt.Errorf("snapshot entry is truncated")
		}
		keyLength := binary.BigEndian.Uint32(payload[cursor : cursor+4])
		cursor += 4
		if len(payload)-cursor < 8 || keyLength > maxKeyLength || uint64(keyLength) > uint64(len(payload)-cursor-8) {
			return snapshot{}, fmt.Errorf("invalid snapshot key length %d", keyLength)
		}
		keyEnd := cursor + int(keyLength)
		key := string(payload[cursor:keyEnd])
		cursor = keyEnd
		if len(payload)-cursor < 8 {
			return snapshot{}, fmt.Errorf("snapshot entry offset is truncated")
		}
		offset := binary.BigEndian.Uint64(payload[cursor : cursor+8])
		cursor += 8
		if offset < uint64(logHeaderSize) || offset >= logPosition || offset > math.MaxInt64 {
			return snapshot{}, fmt.Errorf("invalid snapshot offset for key")
		}
		if _, exists := result.index[key]; exists {
			return snapshot{}, fmt.Errorf("duplicate snapshot key")
		}
		result.index[key] = int64(offset)
	}
	if cursor != len(payload) {
		return snapshot{}, fmt.Errorf("snapshot payload has trailing data")
	}
	return result, nil
}

func writeSnapshot(path string, s snapshot) error {
	data, err := encodeSnapshot(s)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create snapshot temporary file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func(primary error) error {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			return errors.Join(primary, fmt.Errorf("remove snapshot temporary file: %w", err))
		}
		return primary
	}

	if err := writeAll(temp, data); err != nil {
		_ = temp.Close()
		return cleanup(fmt.Errorf("write snapshot temporary file: %w", err))
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return cleanup(fmt.Errorf("sync snapshot temporary file: %w", err))
	}
	if err := temp.Close(); err != nil {
		return cleanup(fmt.Errorf("close snapshot temporary file: %w", err))
	}
	if err := replaceSnapshot(path, tempPath); err != nil {
		return cleanup(fmt.Errorf("publish snapshot: %w", err))
	}
	if err := syncSnapshotDirectory(dir); err != nil {
		return fmt.Errorf("sync snapshot directory: %w", err)
	}
	return nil
}

func writeAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("short write: wrote 0 bytes")
		}
		data = data[n:]
	}
	return nil
}
