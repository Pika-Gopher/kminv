package kminv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

type DB struct {
	log   *logFile
	index map[string]int64

	mu     sync.Mutex
	closed bool
	failed error

	syncLog     func() error
	truncateLog func(int64) error
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
	}
	if err := db.recover(); err != nil {
		_ = log.close()
		return nil, fmt.Errorf("recover database: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}
	if err := db.log.close(); err != nil {
		return err
	}
	db.closed = true
	return nil
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

func (db *DB) recover() error {
	info, err := db.log.file.Stat()
	if err != nil {
		return fmt.Errorf("stat log for recovery: %w", err)
	}

	offset := int64(logHeaderSize)
	for offset < info.Size() {
		remaining := info.Size() - offset
		if remaining < 4 {
			return db.recoverTail(offset)
		}

		var lengthPrefix [4]byte
		if _, err := db.log.file.ReadAt(lengthPrefix[:], offset); err != nil {
			return fmt.Errorf("read record length at offset %d: %w", offset, err)
		}
		recordLength := binary.BigEndian.Uint32(lengthPrefix[:])
		if recordLength < 13 || recordLength > maxRecordLength {
			return fmt.Errorf("invalid record length %d at offset %d", recordLength, offset)
		}
		if int64(recordLength)+4 > remaining {
			return db.recoverTail(offset)
		}

		data, err := db.log.readRecord(offset)
		if err != nil {
			return fmt.Errorf("read record at offset %d: %w", offset, err)
		}
		record, err := decodeRecord(data)
		if err != nil {
			return fmt.Errorf("decode record at offset %d: %w", offset, err)
		}

		switch record.Op {
		case OpSet:
			db.index[string(record.Key)] = offset
		case OpDelete:
			delete(db.index, string(record.Key))
		}
		offset += int64(len(data))
	}

	return nil
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
