package kminv

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	logHeaderSize = 12
	logVersion    = uint32(1)
	logMagic      = "MINIKVDB"
)

type logFile struct {
	file *os.File
	path string
}

func openLog(path string) (*logFile, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %q: %w", path, err)
	}

	log := &logFile{file: file, path: path}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat log %q: %w", path, err)
	}

	if info.Size() == 0 {
		header := make([]byte, logHeaderSize)
		copy(header[:8], logMagic)
		binary.BigEndian.PutUint32(header[8:], logVersion)
		if _, err := file.Write(header); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("write log header %q: %w", path, err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("sync log header %q: %w", path, err)
		}
		return log, nil
	}

	header := make([]byte, logHeaderSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		_ = file.Close()
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("read log header %q: truncated header", path)
		}
		return nil, fmt.Errorf("read log header %q: %w", path, err)
	}
	if string(header[:8]) != logMagic {
		_ = file.Close()
		return nil, fmt.Errorf("validate log %q: invalid magic", path)
	}
	if version := binary.BigEndian.Uint32(header[8:]); version != logVersion {
		_ = file.Close()
		return nil, fmt.Errorf("validate log %q: unsupported version %d", path, version)
	}

	return log, nil
}

func (l *logFile) close() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close log %q: %w", l.path, err)
	}
	return nil
}

func (l *logFile) appendRecord(data []byte) (int64, error) {
	info, err := l.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat log %q before append: %w", l.path, err)
	}

	offset := info.Size()
	n, err := l.file.WriteAt(data, offset)
	if err != nil {
		return 0, fmt.Errorf("append record to log %q: %w", l.path, err)
	}
	if n != len(data) {
		return 0, fmt.Errorf("append record to log %q: short write: wrote %d of %d bytes", l.path, n, len(data))
	}

	return offset, nil
}

func (l *logFile) readRecord(offset int64) ([]byte, error) {
	info, err := l.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat log %q before read: %w", l.path, err)
	}
	if offset < logHeaderSize || offset >= info.Size() {
		return nil, fmt.Errorf("invalid record offset %d in log %q", offset, l.path)
	}

	var lengthPrefix [4]byte
	if _, err := l.file.ReadAt(lengthPrefix[:], offset); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("read record length at offset %d in log %q: truncated length prefix", offset, l.path)
		}
		return nil, fmt.Errorf("read record length at offset %d in log %q: %w", offset, l.path, err)
	}

	recordLength := binary.BigEndian.Uint32(lengthPrefix[:])
	if recordLength < 13 || recordLength > maxRecordLength {
		return nil, fmt.Errorf("invalid record length %d at offset %d in log %q", recordLength, offset, l.path)
	}

	record := make([]byte, 4+int(recordLength))
	copy(record, lengthPrefix[:])
	if _, err := l.file.ReadAt(record[4:], offset+4); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("read record at offset %d in log %q: truncated record", offset, l.path)
		}
		return nil, fmt.Errorf("read record at offset %d in log %q: %w", offset, l.path, err)
	}

	return record, nil
}

func (l *logFile) sync() error {
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync log %q: %w", l.path, err)
	}
	return nil
}

func (l *logFile) truncate(size int64) error {
	if size < logHeaderSize {
		return fmt.Errorf("truncate log %q: size %d is smaller than header size %d", l.path, size, logHeaderSize)
	}
	if err := l.file.Truncate(size); err != nil {
		return fmt.Errorf("truncate log %q to %d bytes: %w", l.path, size, err)
	}
	return nil
}
