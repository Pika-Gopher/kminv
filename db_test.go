package kminv

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestDBGetStoredValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key"), Value: []byte("value")}})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() found = false, want true")
	}
	if !bytes.Equal(value, []byte("value")) {
		t.Fatalf("Get() value = %v, want %q", value, "value")
	}
}

func TestDBGetMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key"), Value: []byte("value")}})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	value, ok, err := db.Get([]byte("missing"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if ok || value != nil {
		t.Fatalf("Get() = (%v, %t), want (nil, false)", value, ok)
	}
}

func TestDBGetEmptyValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: []byte("key")}})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || len(value) != 0 {
		t.Fatalf("Get() = (%v, %t), want an empty value and true", value, ok)
	}
}

func TestDBGetBinaryKeyAndValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	key := []byte{0x00, 0x01, 0xff, 0x00}
	want := []byte{0xff, 0x00, 0x02, 0x00}
	writeRecoveryRecords(t, path, []Record{{Op: OpSet, Key: key, Value: want}})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	value, ok, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || !bytes.Equal(value, want) {
		t.Fatalf("Get() = (%v, %t), want (%v, true)", value, ok, want)
	}
}

func TestDBGetAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	value, ok, err := db.Get([]byte("key"))
	if err == nil {
		t.Fatal("Get() error = nil, want error")
	}
	if ok || value != nil {
		t.Fatalf("Get() = (%v, %t), want (nil, false)", value, ok)
	}
}

func TestDBGetMultipleKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	writeRecoveryRecords(t, path, []Record{
		{Op: OpSet, Key: []byte("one"), Value: []byte("1")},
		{Op: OpSet, Key: []byte("two"), Value: []byte("2")},
	})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "one", value: "1"},
		{key: "two", value: "2"},
	} {
		value, ok, err := db.Get([]byte(test.key))
		if err != nil {
			t.Fatalf("Get(%q) error = %v", test.key, err)
		}
		if !ok || string(value) != test.value {
			t.Fatalf("Get(%q) = (%q, %t), want (%q, true)", test.key, value, ok, test.value)
		}
	}
}

func TestDBSetStoresValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || !bytes.Equal(value, []byte("value")) {
		t.Fatalf("Get() = (%v, %t), want (%q, true)", value, ok, "value")
	}
}

func TestDBSetOverwritesValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("old")); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	if err := db.Set([]byte("key"), []byte("new")); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}
	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || string(value) != "new" {
		t.Fatalf("Get() = (%q, %t), want (%q, true)", value, ok, "new")
	}
}

func TestDBSetEmptyKeyAndValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.Set(nil, nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := db.Get(nil)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || len(value) != 0 {
		t.Fatalf("Get() = (%v, %t), want empty value and true", value, ok)
	}
}

func TestDBSetBinaryKeyAndValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	key := []byte{0x00, 0xff, 0x01, 0x00}
	value := []byte{0xff, 0x00, 0x02}
	if err := db.Set(key, value); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, ok, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || !bytes.Equal(got, value) {
		t.Fatalf("Get() = (%v, %t), want (%v, true)", got, ok, value)
	}
}

func TestDBSetRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.Set(make([]byte, 1025), nil); err == nil {
		t.Fatal("Set() with oversized key error = nil, want error")
	}
	if err := db.Set(nil, make([]byte, 1<<20+1)); err == nil {
		t.Fatal("Set() with oversized value error = nil, want error")
	}
}

func TestDBSetAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := db.Set([]byte("key"), []byte("value")); err == nil {
		t.Fatal("Set() error = nil, want error")
	}
}

func TestDBSetFailureDoesNotUpdateIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.log.file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}
	if err := db.Set([]byte("key"), []byte("value")); err == nil {
		t.Fatal("Set() error = nil, want error")
	}
	if _, ok := db.index["key"]; ok {
		t.Fatal("failed Set updated the index")
	}
}

func TestDBDeleteExistingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := db.Delete([]byte("key")); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if value, ok, err := db.Get([]byte("key")); err != nil {
		t.Fatalf("Get() error = %v", err)
	} else if ok || value != nil {
		t.Fatalf("Get() = (%v, %t), want (nil, false)", value, ok)
	}
}

func TestDBDeleteNonexistentKey(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	before, err := db.log.file.Stat()
	if err != nil {
		t.Fatalf("Stat() before Delete() error = %v", err)
	}
	syncCalls := 0
	db.syncLog = func() error {
		syncCalls++
		return errors.New("sync should not be called")
	}
	if err := db.Delete([]byte("missing")); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	after, err := db.log.file.Stat()
	if err != nil {
		t.Fatalf("Stat() after Delete() error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("log size after Delete() = %d, want unchanged size %d", after.Size(), before.Size())
	}
	if syncCalls != 0 {
		t.Fatalf("sync calls = %d, want 0", syncCalls)
	}
	if _, ok, err := db.Get([]byte("missing")); err != nil {
		t.Fatalf("Get() error = %v", err)
	} else if ok {
		t.Fatal("deleted nonexistent key is present")
	}
}

func TestDBDeleteSameKeyTwice(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := db.Delete([]byte("key")); err != nil {
		t.Fatalf("first Delete() error = %v", err)
	}
	if err := db.Delete([]byte("key")); err != nil {
		t.Fatalf("second Delete() error = %v", err)
	}
}

func TestDBDeletePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := db.Delete([]byte("key")); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db = openTestDB(t, path)
	defer db.Close()
	if _, ok, err := db.Get([]byte("key")); err != nil {
		t.Fatalf("Get() error = %v", err)
	} else if ok {
		t.Fatal("deleted key is present after reopen")
	}
}

func TestDBDeleteEmptyAndBinaryKeys(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	keys := [][]byte{nil, {0x00, 0xff, 0x00}}
	for _, key := range keys {
		if err := db.Set(key, []byte("value")); err != nil {
			t.Fatalf("Set(%v) error = %v", key, err)
		}
		if err := db.Delete(key); err != nil {
			t.Fatalf("Delete(%v) error = %v", key, err)
		}
		if _, ok, err := db.Get(key); err != nil {
			t.Fatalf("Get(%v) error = %v", key, err)
		} else if ok {
			t.Fatalf("key %v is present after deletion", key)
		}
	}
}

func TestDBDeleteAfterClose(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := db.Delete([]byte("key")); err == nil {
		t.Fatal("Delete() error = nil, want error")
	}
}

func TestDBDeleteRejectsOversizedKey(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	if err := db.Delete(make([]byte, 1025)); err == nil {
		t.Fatal("Delete() error = nil, want oversized-key error")
	}
}

func TestDBDeleteFailurePreservesIndex(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := db.log.file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}
	if err := db.Delete([]byte("key")); err == nil {
		t.Fatal("Delete() error = nil, want error")
	}
	if _, ok := db.index["key"]; !ok {
		t.Fatal("failed Delete removed the index entry")
	}
}

func TestDBSetSuccessfulRollbackPreservesState(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("old")); err != nil {
		t.Fatalf("initial Set() error = %v", err)
	}
	previousOffset := db.index["key"]
	originalSync := db.log.sync
	syncCalls := 0
	syncFailure := errors.New("injected sync failure")
	db.syncLog = func() error {
		syncCalls++
		if syncCalls == 1 {
			return syncFailure
		}
		return originalSync()
	}

	if err := db.Set([]byte("key"), []byte("new")); !errors.Is(err, syncFailure) {
		t.Fatalf("Set() error = %v, want sync failure", err)
	}
	if db.failed != nil {
		t.Fatalf("db.failed = %v, want nil after successful rollback", db.failed)
	}
	if got := db.index["key"]; got != previousOffset {
		t.Fatalf("index[key] = %d, want %d", got, previousOffset)
	}
	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || string(value) != "old" {
		t.Fatalf("Get() = (%q, %t), want (%q, true)", value, ok, "old")
	}
}

func TestDBRollbackFailureMarksDatabaseFailed(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	if err := db.Set([]byte("key"), []byte("old")); err != nil {
		t.Fatalf("initial Set() error = %v", err)
	}
	previousOffset := db.index["key"]
	syncFailure := errors.New("injected sync failure")
	rollbackFailure := errors.New("injected rollback failure")
	db.syncLog = func() error {
		return syncFailure
	}
	db.truncateLog = func(int64) error {
		return rollbackFailure
	}

	err := db.Set([]byte("key"), []byte("new"))
	if err == nil || !errors.Is(err, syncFailure) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("Set() error = %v, want both mutation and rollback failures", err)
	}
	if db.failed == nil {
		t.Fatal("db.failed = nil, want failed state")
	}
	if got := db.index["key"]; got != previousOffset {
		t.Fatalf("index[key] = %d, want %d", got, previousOffset)
	}

	if _, _, err := db.Get([]byte("key")); err == nil {
		t.Fatal("Get() error = nil, want failed-database error")
	}
	if err := db.Set([]byte("other"), []byte("value")); err == nil {
		t.Fatal("Set() error = nil, want failed-database error")
	}
	if err := db.Delete([]byte("key")); err == nil {
		t.Fatal("Delete() error = nil, want failed-database error")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestDBDeleteRollbackFailurePreservesIndex(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("initial Set() error = %v", err)
	}
	previousOffset := db.index["key"]
	syncFailure := errors.New("injected sync failure")
	db.syncLog = func() error {
		return syncFailure
	}
	db.truncateLog = func(int64) error {
		return errors.New("injected rollback failure")
	}

	if err := db.Delete([]byte("key")); err == nil {
		t.Fatal("Delete() error = nil, want error")
	}
	if _, ok := db.index["key"]; !ok {
		t.Fatal("failed Delete removed the index entry")
	}
	if got := db.index["key"]; got != previousOffset {
		t.Fatalf("index[key] = %d, want %d", got, previousOffset)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func openTestDB(t *testing.T, path string) *DB {
	t.Helper()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return db
}

func TestDBSetPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db = openTestDB(t, path)
	defer db.Close()
	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || !bytes.Equal(value, []byte("value")) {
		t.Fatalf("Get() = (%q, %t), want (%q, true)", value, ok, "value")
	}
}

func TestDBRecoveryPreservesFinalMixedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.log")
	db := openTestDB(t, path)
	operations := []struct {
		key    string
		value  string
		delete bool
	}{
		{key: "one", value: "old"},
		{key: "one", value: "new"},
		{key: "two", value: "value"},
		{key: "two", delete: true},
		{key: "three", value: "first"},
		{key: "three", delete: true},
		{key: "three", value: "final"},
	}
	for _, operation := range operations {
		var err error
		if operation.delete {
			err = db.Delete([]byte(operation.key))
		} else {
			err = db.Set([]byte(operation.key), []byte(operation.value))
		}
		if err != nil {
			t.Fatalf("mutation for %q error = %v", operation.key, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db = openTestDB(t, path)
	defer db.Close()
	tests := []struct {
		key   string
		value string
		found bool
	}{
		{key: "one", value: "new", found: true},
		{key: "two", found: false},
		{key: "three", value: "final", found: true},
	}
	for _, test := range tests {
		value, found, err := db.Get([]byte(test.key))
		if err != nil {
			t.Fatalf("Get(%q) error = %v", test.key, err)
		}
		if found != test.found || (found && string(value) != test.value) {
			t.Fatalf("Get(%q) = (%q, %t), want (%q, %t)", test.key, value, found, test.value, test.found)
		}
	}
}

func TestDBGetReturnedValueIsIndependent(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	if err := db.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	if !ok {
		t.Fatal("first Get() found = false, want true")
	}
	value[0] = 'X'

	value, ok, err = db.Get([]byte("key"))
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if !ok || string(value) != "value" {
		t.Fatalf("second Get() = (%q, %t), want (%q, true)", value, ok, "value")
	}
}

func TestDBConcurrentGetSetDelete(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "data.log"))
	defer db.Close()

	const workers = 8
	const iterations = 20
	errs := make(chan error, workers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)

	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer waitGroup.Done()
			key := []byte{byte(worker)}
			for iteration := 0; iteration < iterations; iteration++ {
				value := []byte{byte(iteration)}
				if err := db.Set(key, value); err != nil {
					errs <- err
					return
				}
				if _, _, err := db.Get(key); err != nil {
					errs <- err
					return
				}
				if err := db.Delete(key); err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}

	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent database operation error = %v", err)
	}
}
