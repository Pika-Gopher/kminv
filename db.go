package kminv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
)

type DB struct {
	log   *logFile
	index map[string]int64

	mu     sync.Mutex
	closed bool
	failed error

	syncLog         func() error
	truncateLog     func(int64) error
	persistSnapshot func(snapshot) error
	closeLog        func() error
}

func Open(path string) (*DB, error) {
	log, err := openLog(path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db := &DB{
		log:         log,
		index:       make(map[string]int64),
		syncLog:     log.sync,
		truncateLog: log.truncate,
		persistSnapshot: func(s snapshot) error {
			return writeSnapshot(log.path+".index", s)
		},
		closeLog: log.close,
	}
	validation, err := db.recover()
	if err != nil {
		_ = log.close()
		return nil, fmt.Errorf("recover database: %w", err)
	}
	if data, err := os.ReadFile(log.path + ".index"); err == nil {
		if s, err := decodeSnapshot(data); err == nil {
			if err := db.validateSnapshotWith(s, validation); err == nil {
				db.index = cloneIndex(s.index)
				if err := db.replayRange(s.logPosition, validation.end, db.index); err != nil {
					_ = log.close()
					return nil, fmt.Errorf("replay snapshot suffix: %w", err)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		// Snapshot I/O failures are disposable derived-state failures.
	}
	return db, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}
	db.closed = true
	var snapshotErr error
	if db.failed == nil {
		info, err := db.log.file.Stat()
		if err != nil {
			snapshotErr = fmt.Errorf("stat log for snapshot: %w", err)
		} else {
			snapshotErr = db.persistSnapshot(snapshot{
				logPosition: info.Size(),
				index:       cloneIndex(db.index),
			})
		}
	}
	closeErr := db.closeLog()
	return errors.Join(snapshotErr, closeErr)
}

func (db *DB) Get(key []byte) ([]byte, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil, false, fmt.Errorf("database is closed")
	}
	if db.failed != nil {
		return nil, false, fmt.Errorf("database has failed: %w", db.failed)
	}

	offset, ok := db.index[string(key)]
	if !ok {
		return nil, false, nil
	}

	data, err := db.log.readRecord(offset)
	if err != nil {
		return nil, false, fmt.Errorf("read record for key at offset %d: %w", offset, err)
	}
	record, err := decodeRecord(data)
	if err != nil {
		return nil, false, fmt.Errorf("decode record for key at offset %d: %w", offset, err)
	}
	if record.Op != OpSet {
		return nil, false, fmt.Errorf("indexed record at offset %d is not a SET record", offset)
	}
	if string(record.Key) != string(key) {
		return nil, false, fmt.Errorf("indexed record at offset %d has a different key", offset)
	}

	return record.Value, true, nil
}

func (db *DB) Set(key, value []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return fmt.Errorf("database is closed")
	}
	if db.failed != nil {
		return fmt.Errorf("database has failed: %w", db.failed)
	}

	record := Record{
		Op:    OpSet,
		Key:   append([]byte(nil), key...),
		Value: append([]byte(nil), value...),
	}
	data, err := encodeRecord(record)
	if err != nil {
		return fmt.Errorf("encode SET record: %w", err)
	}

	info, err := db.log.file.Stat()
	if err != nil {
		return fmt.Errorf("stat log before SET: %w", err)
	}
	previousEnd := info.Size()

	offset, err := db.log.appendRecord(data)
	if err != nil {
		return db.rollbackMutation(previousEnd, fmt.Errorf("append SET record: %w", err))
	}
	if err := db.syncLog(); err != nil {
		return db.rollbackMutation(previousEnd, fmt.Errorf("sync SET record: %w", err))
	}

	db.index[string(record.Key)] = offset
	return nil
}

// Delete is idempotent: deleting an absent key succeeds without appending.
func (db *DB) Delete(key []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return fmt.Errorf("database is closed")
	}
	if db.failed != nil {
		return fmt.Errorf("database has failed: %w", db.failed)
	}
	record := Record{
		Op:  OpDelete,
		Key: append([]byte(nil), key...),
	}
	data, err := encodeRecord(record)
	if err != nil {
		return fmt.Errorf("encode DELETE record: %w", err)
	}
	if _, ok := db.index[string(record.Key)]; !ok {
		return nil
	}

	info, err := db.log.file.Stat()
	if err != nil {
		return fmt.Errorf("stat log before DELETE: %w", err)
	}
	previousEnd := info.Size()

	_, err = db.log.appendRecord(data)
	if err != nil {
		return db.rollbackMutation(previousEnd, fmt.Errorf("append DELETE record: %w", err))
	}
	if err := db.syncLog(); err != nil {
		return db.rollbackMutation(previousEnd, fmt.Errorf("sync DELETE record: %w", err))
	}

	delete(db.index, string(record.Key))
	return nil
}

func (db *DB) rollbackMutation(previousEnd int64, operationErr error) error {
	if err := db.truncateLog(previousEnd); err != nil {
		db.failed = errors.Join(operationErr, fmt.Errorf("rollback failed: %w", err))
		return db.failed
	}
	if err := db.syncLog(); err != nil {
		db.failed = errors.Join(operationErr, fmt.Errorf("rollback sync failed: %w", err))
		return db.failed
	}
	return operationErr
}

func (db *DB) recover() (logValidation, error) {
	validation, err := db.scanLog(0, true, true, func(offset int64, record Record) {
		switch record.Op {
		case OpSet:
			db.index[string(record.Key)] = offset
		case OpDelete:
			delete(db.index, string(record.Key))
		}
	})
	return validation, err
}

type logValidation struct {
	end        int64
	boundaries []int64
}

func (db *DB) validateLog() (logValidation, error) {
	return db.scanLog(0, false, true, nil)
}

func (db *DB) validateSnapshot(s snapshot) error {
	validation, err := db.validateLog()
	if err != nil {
		return fmt.Errorf("validate authoritative log: %w", err)
	}
	return db.validateSnapshotWith(s, validation)
}

func (db *DB) validateSnapshotWith(s snapshot, validation logValidation) error {
	if s.logPosition > validation.end {
		return fmt.Errorf("snapshot log position %d exceeds validated log end %d", s.logPosition, validation.end)
	}
	if !validation.hasBoundary(s.logPosition) {
		return fmt.Errorf("snapshot log position %d is not a record boundary", s.logPosition)
	}

	for key, offset := range s.index {
		if offset >= s.logPosition || !validation.hasBoundary(offset) {
			return fmt.Errorf("snapshot offset %d for key is not a valid record boundary", offset)
		}
		data, err := db.log.readRecord(offset)
		if err != nil {
			return fmt.Errorf("read snapshot record at offset %d: %w", offset, err)
		}
		record, err := decodeRecord(data)
		if err != nil {
			return fmt.Errorf("decode snapshot record at offset %d: %w", offset, err)
		}
		if record.Op != OpSet {
			return fmt.Errorf("snapshot entry for key points to a non-SET record at offset %d", offset)
		}
		if string(record.Key) != key {
			return fmt.Errorf("snapshot entry key does not match record at offset %d", offset)
		}
	}

	prefixIndex := make(map[string]int64)
	if err := db.replayRange(logHeaderSize, s.logPosition, prefixIndex); err != nil {
		return fmt.Errorf("replay log prefix for snapshot: %w", err)
	}
	if !indexesEqual(prefixIndex, s.index) {
		return fmt.Errorf("snapshot index does not match log prefix at position %d", s.logPosition)
	}
	return nil
}

func (db *DB) replayRange(start, end int64, index map[string]int64) error {
	_, err := db.scanLogRange(start, end, false, false, func(offset int64, record Record) {
		switch record.Op {
		case OpSet:
			index[string(record.Key)] = offset
		case OpDelete:
			delete(index, string(record.Key))
		}
	})
	return err
}

func cloneIndex(index map[string]int64) map[string]int64 {
	clone := make(map[string]int64, len(index))
	for key, offset := range index {
		clone[key] = offset
	}
	return clone
}

func indexesEqual(left, right map[string]int64) bool {
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

func (db *DB) scanLog(limit int64, recoverTail bool, collectBoundaries bool, apply func(int64, Record)) (logValidation, error) {
	return db.scanLogRange(logHeaderSize, limit, recoverTail, collectBoundaries, apply)
}

func (db *DB) scanLogRange(start, limit int64, recoverTail bool, collectBoundaries bool, apply func(int64, Record)) (logValidation, error) {
	info, err := db.log.file.Stat()
	if err != nil {
		return logValidation{}, fmt.Errorf("stat log for recovery: %w", err)
	}
	end := info.Size()
	if limit != 0 {
		if limit < logHeaderSize || limit > end {
			return logValidation{}, fmt.Errorf("invalid log scan limit %d", limit)
		}
		end = limit
	}

	if start < logHeaderSize || start > end {
		return logValidation{}, fmt.Errorf("invalid log scan start %d", start)
	}
	offset := start
	var boundaries []int64
	if collectBoundaries {
		boundaries = append(boundaries, offset)
	}
	for offset < end {
		remaining := end - offset
		if remaining < 4 {
			if !recoverTail {
				return logValidation{}, fmt.Errorf("log scan ended with an incomplete record at offset %d", offset)
			}
			if err := db.recoverTail(offset); err != nil {
				return logValidation{}, err
			}
			return logValidation{end: offset, boundaries: boundaries}, nil
		}

		var lengthPrefix [4]byte
		if _, err := db.log.file.ReadAt(lengthPrefix[:], offset); err != nil {
			return logValidation{}, fmt.Errorf("read record length at offset %d: %w", offset, err)
		}
		recordLength := binary.BigEndian.Uint32(lengthPrefix[:])
		if recordLength < 13 || recordLength > maxRecordLength {
			return logValidation{}, fmt.Errorf("invalid record length %d at offset %d", recordLength, offset)
		}
		if int64(recordLength)+4 > remaining {
			if !recoverTail {
				return logValidation{}, fmt.Errorf("log scan limit %d is inside an incomplete record", limit)
			}
			if err := db.recoverTail(offset); err != nil {
				return logValidation{}, err
			}
			return logValidation{end: offset, boundaries: boundaries}, nil
		}

		data, err := db.log.readRecord(offset)
		if err != nil {
			return logValidation{}, fmt.Errorf("read record at offset %d: %w", offset, err)
		}
		record, err := decodeRecord(data)
		if err != nil {
			return logValidation{}, fmt.Errorf("decode record at offset %d: %w", offset, err)
		}

		if apply != nil {
			apply(offset, record)
		}
		offset += int64(len(data))
		if collectBoundaries {
			boundaries = append(boundaries, offset)
		}
	}

	return logValidation{end: offset, boundaries: boundaries}, nil
}

func (v logValidation) hasBoundary(offset int64) bool {
	index, found := slices.BinarySearch(v.boundaries, offset)
	return found && v.boundaries[index] == offset
}

func (db *DB) recoverTail(offset int64) error {
	if err := db.log.truncate(offset); err != nil {
		return fmt.Errorf("truncate recoverable log tail at offset %d: %w", offset, err)
	}
	if err := db.log.sync(); err != nil {
		return fmt.Errorf("sync recovered log tail at offset %d: %w", offset, err)
	}
	return nil
}
