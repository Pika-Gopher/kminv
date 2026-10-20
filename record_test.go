package kminv

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func TestEncodeRecordSet(t *testing.T) {
	record := Record{
		Op:    OpSet,
		Key:   []byte("user"),
		Value: []byte("alice"),
	}

	got, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	if gotLen, want := len(got), 17+len(record.Key)+len(record.Value); gotLen != want {
		t.Fatalf("encoded length = %d, want %d", gotLen, want)
	}

	// Verify the encoded record layout and checksum.
	if gotLength, want := binary.BigEndian.Uint32(got[0:4]), uint32(len(got)-4); gotLength != want {
		t.Fatalf("record length = %d, want %d", gotLength, want)
	}
	if got[8] != OpSet {
		t.Fatalf("operation = %#x, want %#x", got[8], OpSet)
	}
	if gotKeyLength := binary.BigEndian.Uint32(got[9:13]); gotKeyLength != uint32(len(record.Key)) {
		t.Fatalf("key length = %d, want %d", gotKeyLength, len(record.Key))
	}
	if gotValueLength := binary.BigEndian.Uint32(got[13:17]); gotValueLength != uint32(len(record.Value)) {
		t.Fatalf("value length = %d, want %d", gotValueLength, len(record.Value))
	}
	if string(got[17:17+len(record.Key)]) != string(record.Key) {
		t.Fatalf("key = %q, want %q", got[17:17+len(record.Key)], record.Key)
	}
	if string(got[17+len(record.Key):]) != string(record.Value) {
		t.Fatalf("value = %q, want %q", got[17+len(record.Key):], record.Value)
	}
	if gotChecksum, want := binary.BigEndian.Uint32(got[4:8]), crc32.ChecksumIEEE(got[8:]); gotChecksum != want {
		t.Fatalf("checksum = %#x, want %#x", gotChecksum, want)
	}
}

func TestEncodeRecordDelete(t *testing.T) {
	record := Record{Op: OpDelete, Key: []byte("user")}

	got, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	if got[8] != OpDelete {
		t.Fatalf("operation = %#x, want %#x", got[8], OpDelete)
	}
	if gotValueLength := binary.BigEndian.Uint32(got[13:17]); gotValueLength != 0 {
		t.Fatalf("value length = %d, want 0", gotValueLength)
	}
	if len(got) != 17+len(record.Key) {
		t.Fatalf("encoded length = %d, want %d", len(got), 17+len(record.Key))
	}
}

func TestEncodeRecordEmptyKeyAndValue(t *testing.T) {
	got, err := encodeRecord(Record{Op: OpSet})
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	if len(got) != 17 {
		t.Fatalf("encoded length = %d, want 17", len(got))
	}
	if gotKeyLength := binary.BigEndian.Uint32(got[9:13]); gotKeyLength != 0 {
		t.Fatalf("key length = %d, want 0", gotKeyLength)
	}
	if gotValueLength := binary.BigEndian.Uint32(got[13:17]); gotValueLength != 0 {
		t.Fatalf("value length = %d, want 0", gotValueLength)
	}
}

func TestEncodeRecordInvalidOperation(t *testing.T) {
	_, err := encodeRecord(Record{Op: 0xff})
	if err == nil {
		t.Fatal("encodeRecord() error = nil, want error")
	}
}

func TestEncodeRecordKeyTooLarge(t *testing.T) {
	_, err := encodeRecord(Record{Op: OpSet, Key: make([]byte, 1025)})
	if err == nil {
		t.Fatal("encodeRecord() error = nil, want error")
	}
}

func TestEncodeRecordValueTooLarge(t *testing.T) {
	_, err := encodeRecord(Record{Op: OpSet, Value: make([]byte, 1<<20+1)})
	if err == nil {
		t.Fatal("encodeRecord() error = nil, want error")
	}
}

func TestEncodeRecordDeleteWithValue(t *testing.T) {
	_, err := encodeRecord(Record{
		Op:    OpDelete,
		Value: []byte("not empty"),
	})
	if err == nil {
		t.Fatal("encodeRecord() error = nil, want error")
	}
}

func TestDecodeRecordSet(t *testing.T) {
	want := Record{Op: OpSet, Key: []byte("user"), Value: []byte("alice")}
	data, err := encodeRecord(want)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	got, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if got.Op != want.Op || !bytes.Equal(got.Key, want.Key) || !bytes.Equal(got.Value, want.Value) {
		t.Fatalf("decoded record = %#v, want %#v", got, want)
	}
}

func TestDecodeRecordDelete(t *testing.T) {
	want := Record{Op: OpDelete, Key: []byte("user")}
	data, err := encodeRecord(want)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	got, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if got.Op != want.Op || !bytes.Equal(got.Key, want.Key) || len(got.Value) != 0 {
		t.Fatalf("decoded record = %#v, want %#v", got, want)
	}
}

func TestDecodeRecordEmptyKeyAndValue(t *testing.T) {
	want := Record{Op: OpSet}
	data, err := encodeRecord(want)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	got, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if got.Op != want.Op || len(got.Key) != 0 || len(got.Value) != 0 {
		t.Fatalf("decoded record = %#v, want %#v", got, want)
	}
}

func TestDecodeRecordBinaryKeyAndValue(t *testing.T) {
	want := Record{
		Op:    OpSet,
		Key:   []byte{0x00, 0x01, 0xff, 0x00},
		Value: []byte{0xff, 0x00, 0x02, 0x00},
	}
	data, err := encodeRecord(want)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}

	got, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if !bytes.Equal(got.Key, want.Key) || !bytes.Equal(got.Value, want.Value) {
		t.Fatalf("decoded record = %#v, want %#v", got, want)
	}
}

func TestDecodeRecordMalformed(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{
			name:    "short input",
			data:    make([]byte, 16),
			wantErr: "record is too short: 16 bytes",
		},
		{
			name: "incorrect record length",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet, Key: []byte("key")})
				binary.BigEndian.PutUint32(data[0:4], binary.BigEndian.Uint32(data[0:4])+1)
				return data
			}(),
			wantErr: "record length mismatch",
		},
		{
			name: "record exceeds maximum",
			data: func() []byte {
				data := make([]byte, int(maxRecordLength)+5)
				binary.BigEndian.PutUint32(data[0:4], maxRecordLength+1)
				return data
			}(),
			wantErr: "record is too large",
		},
		{
			name: "unknown operation",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet})
				data[8] = 0xff
				refreshRecordChecksum(data)
				return data
			}(),
			wantErr: "invalid record operation: 255",
		},
		{
			name: "key too large",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet})
				binary.BigEndian.PutUint32(data[9:13], 1025)
				refreshRecordChecksum(data)
				return data
			}(),
			wantErr: "key is too large: 1025 bytes",
		},
		{
			name: "value too large",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet})
				binary.BigEndian.PutUint32(data[13:17], 1<<20+1)
				refreshRecordChecksum(data)
				return data
			}(),
			wantErr: "value is too large: 1048577 bytes",
		},
		{
			name: "delete with nonempty value",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet, Value: []byte("value")})
				data[8] = OpDelete
				refreshRecordChecksum(data)
				return data
			}(),
			wantErr: "delete record must have an empty value",
		},
		{
			name: "inconsistent key and value lengths",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet, Key: []byte("key")})
				binary.BigEndian.PutUint32(data[9:13], 3)
				binary.BigEndian.PutUint32(data[13:17], 1)
				refreshRecordChecksum(data)
				return data
			}(),
			wantErr: "record payload length mismatch",
		},
		{
			name: "incorrect checksum",
			data: func() []byte {
				data := encodedRecordForDecodeTest(t, Record{Op: OpSet, Key: []byte("key")})
				data[4] ^= 0xff
				return data
			}(),
			wantErr: "record checksum mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRecord(test.data)
			if err == nil {
				t.Fatal("decodeRecord() error = nil, want error")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(test.wantErr)) {
				t.Fatalf("decodeRecord() error = %q, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestDecodeRecordReturnedDataIsIndependent(t *testing.T) {
	data := encodedRecordForDecodeTest(t, Record{
		Op:    OpSet,
		Key:   []byte("key"),
		Value: []byte("value"),
	})
	wantKey := append([]byte(nil), data[17:20]...)
	wantValue := append([]byte(nil), data[20:]...)

	got, err := decodeRecord(data)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}

	for i := range data {
		data[i] = 0
	}
	if !bytes.Equal(got.Key, wantKey) || !bytes.Equal(got.Value, wantValue) {
		t.Fatalf("decoded data changed after input mutation: got key %v, value %v", got.Key, got.Value)
	}
}

func encodedRecordForDecodeTest(t *testing.T, record Record) []byte {
	t.Helper()
	data, err := encodeRecord(record)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	return data
}

func refreshRecordChecksum(data []byte) {
	binary.BigEndian.PutUint32(data[4:8], crc32.ChecksumIEEE(data[8:]))
}
