package kminv

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	benchmarkKeySize   = 16
	benchmarkValueSize = 256
)

func BenchmarkDBSet(b *testing.B) {
	db, key, value := openBenchmarkDB(b, 1)
	defer db.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		info, err := db.log.file.Stat()
		if err != nil {
			b.Fatal(err)
		}
		previousEnd := info.Size()
		previousOffset := db.index[string(key)]
		b.StartTimer()

		if err := db.Set(key, value); err != nil {
			b.StopTimer()
			b.Fatal(err)
		}

		b.StopTimer()
		if err := db.log.truncate(previousEnd); err != nil {
			b.Fatal(err)
		}
		if err := db.log.sync(); err != nil {
			b.Fatal(err)
		}
		db.index[string(key)] = previousOffset
		b.StartTimer()
	}
}

func BenchmarkDBGetExisting(b *testing.B) {
	db, key, _ := openBenchmarkDB(b, 1)
	defer db.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value, ok, err := db.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		if !ok || len(value) != benchmarkValueSize {
			b.Fatalf("Get() = (%d bytes, %t), want (%d bytes, true)", len(value), ok, benchmarkValueSize)
		}
	}
}

func BenchmarkDBGetMissing(b *testing.B) {
	db, _, _ := openBenchmarkDB(b, 1)
	defer db.Close()
	missingKey := []byte("missing-key-0000")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value, ok, err := db.Get(missingKey)
		if err != nil {
			b.Fatal(err)
		}
		if ok || value != nil {
			b.Fatalf("Get() = (%v, %t), want (nil, false)", value, ok)
		}
	}
}

func BenchmarkDBDeleteExisting(b *testing.B) {
	db, key, _ := openBenchmarkDB(b, 1)
	defer db.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		info, err := db.log.file.Stat()
		if err != nil {
			b.Fatal(err)
		}
		previousEnd := info.Size()
		previousOffset := db.index[string(key)]
		b.StartTimer()

		if err := db.Delete(key); err != nil {
			b.StopTimer()
			b.Fatal(err)
		}

		b.StopTimer()
		if err := db.log.truncate(previousEnd); err != nil {
			b.Fatal(err)
		}
		if err := db.log.sync(); err != nil {
			b.Fatal(err)
		}
		db.index[string(key)] = previousOffset
		b.StartTimer()
	}
}

func BenchmarkDBGetScaling(b *testing.B) {
	for _, count := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("%d-keys", count), func(b *testing.B) {
			db, _, _ := openBenchmarkDB(b, count)
			defer db.Close()
			keys := benchmarkKeys(count)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				value, ok, err := db.Get(keys[i%len(keys)])
				if err != nil {
					b.Fatal(err)
				}
				if !ok || len(value) != benchmarkValueSize {
					b.Fatalf("Get() = (%d bytes, %t), want (%d bytes, true)", len(value), ok, benchmarkValueSize)
				}
			}
		})
	}
}

func BenchmarkDBIndexHeapPerKey(b *testing.B) {
	for _, count := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("%d-keys", count), func(b *testing.B) {
			b.StopTimer()
			path := createBenchmarkFixture(b, count, false)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			db, err := Open(path)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			runtime.GC()
			runtime.ReadMemStats(&after)
			hold := db
			runtime.KeepAlive(hold)
			delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if delta < 0 {
				delta = 0
			}
			b.ReportMetric(float64(delta)/float64(count), "heap-bytes/key")
			b.ReportMetric(float64(len(db.index)), "index-keys")
			b.Cleanup(func() {
				_ = os.Remove(path)
				_ = os.Remove(path + ".index")
			})
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				runtime.KeepAlive(hold)
			}
		})
	}
}

func BenchmarkDBOpenWithoutSnapshot(b *testing.B) {
	benchmarkOpen(b, false)
}

func BenchmarkDBOpenWithSnapshot(b *testing.B) {
	benchmarkOpen(b, true)
}

func benchmarkOpen(b *testing.B, withSnapshot bool) {
	for _, count := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("%d-keys", count), func(b *testing.B) {
			path := createBenchmarkFixture(b, count, true)
			snapshotPath := path + ".index"
			if !withSnapshot {
				removeBenchmarkSnapshot(b, snapshotPath)
			}
			b.Cleanup(func() {
				_ = os.Remove(path)
				_ = os.Remove(snapshotPath)
			})

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !withSnapshot {
					removeBenchmarkSnapshot(b, snapshotPath)
				}
				db, err := Open(path)
				if err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := db.Close(); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}

func removeBenchmarkSnapshot(b *testing.B, path string) {
	b.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		b.Fatal(err)
	}
}

func openBenchmarkDB(b *testing.B, count int) (*DB, []byte, []byte) {
	b.Helper()
	path := createBenchmarkFixture(b, count, false)
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(path)
		_ = os.Remove(path + ".index")
	})
	key := benchmarkKeys(1)[0]
	value := bytes.Repeat([]byte{'v'}, benchmarkValueSize)
	return db, key, value
}

func createBenchmarkFixture(b *testing.B, count int, withSnapshot bool) string {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "benchmark.log")
	log, err := openLog(path)
	if err != nil {
		b.Fatal(err)
	}
	keys := benchmarkKeys(count)
	value := bytes.Repeat([]byte{'v'}, benchmarkValueSize)
	for _, key := range keys {
		data, err := encodeRecord(Record{Op: OpSet, Key: key, Value: value})
		if err != nil {
			_ = log.close()
			b.Fatal(err)
		}
		if _, err := log.appendRecord(data); err != nil {
			_ = log.close()
			b.Fatal(err)
		}
	}
	if err := log.sync(); err != nil {
		_ = log.close()
		b.Fatal(err)
	}
	if err := log.close(); err != nil {
		b.Fatal(err)
	}
	if withSnapshot {
		db, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	}
	return path
}

func benchmarkKeys(count int) [][]byte {
	keys := make([][]byte, count)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("key-%0*d", benchmarkKeySize-len("key-"), i))
	}
	return keys
}
