# kminv

kminv is a small embedded, single-process Go key-value store. It accepts
arbitrary byte-oriented keys and values, persists mutations in an append-only
binary log, and keeps an in-memory key-to-record-offset index. V1 includes
crash-tail recovery, authoritative-log validation, and disposable index
snapshots.

## Features

- Embedded Go package with no server process.
- Single active process/instance assumption; no cross-process locking.
- Arbitrary `[]byte` keys and values, including empty and binary data.
- Append-only durable log with checksummed records.
- In-memory offset index; values remain in the log rather than the index.
- Recovery that truncates only clearly incomplete final tails and rejects
  complete corruption.
- Snapshot loading, exact-prefix consistency validation, suffix replay, and
  full-log rebuild fallback.
- Snapshot persistence during `Close()` using temporary files and atomic
  replacement helpers.

## Installation and usage

The module path is `kminv`. From another Go module, import it with:

```go
import "kminv"
```

Example:

```go
package main

import (
	"fmt"
	"log"

	"kminv"
)

func main() {
	db, err := kminv.Open("data.log")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Fatal(err)
		}
	}()

	if err := db.Set([]byte("language"), []byte("Go")); err != nil {
		log.Fatal(err)
	}

	value, ok, err := db.Get([]byte("language"))
	if err != nil {
		log.Fatal(err)
	}
	if ok {
		fmt.Println(string(value))
	}

	if err := db.Delete([]byte("language")); err != nil {
		log.Fatal(err)
	}
}
```

Within this repository, run the package tests with `go test ./...`.

## Behavior and limitations

- Keys are limited to 1,024 bytes.
- Values are limited to 1 MiB.
- Empty keys, empty values, and binary bytes are supported.
- The log is authoritative; snapshots are disposable derived state.
- Each successful mutation is appended and synchronized before the in-memory
  index is changed.
- `File.Sync` is the practical durability boundary, not a guarantee against
  every hardware or filesystem failure.
- Only one active database instance or process should access a database path.
- `Close()` is terminal. Snapshot and log-close errors are reported, and the
  log close is attempted even when snapshot persistence fails.
- Snapshot files are stored beside the log at `<log path>.index`. A missing,
  malformed, stale, or inconsistent snapshot is ignored and the index is
  rebuilt from the recovered log.
- Startup validates the complete authoritative log even when a snapshot is
  available. Snapshots reduce index replay work but do not eliminate the log
  scan.
- Unix-like systems sync the snapshot's parent directory after replacement.
  Windows uses platform-specific replacement helpers but does not sync a
  directory handle because that operation is not reliably supported there.

## Testing

Run the regular suite:

```text
go test -v -count=1 ./...
```

Run the race suite:

```text
go test -race -count=1 ./...
```

The regular and race suites pass in the current development Windows
environment. Results can vary with the local Go toolchain and platform.

## Benchmark baseline

The benchmark source is [db_bench_test.go](./db_bench_test.go). Run it with:

```text
go test -run '^$' -bench . -benchmem -count=3 ./...
```

The following approximate ranges were observed on Windows/amd64 with an Intel
13th Gen Core i7-13700HX. They are baseline measurements, not universal
performance guarantees. Results depend on the machine, operating system, Go
version, filesystem, and benchmark conditions.

| Benchmark | Observed result |
|---|---|
| Set | 1.04–1.23 ms/op; 833–834 B/op; 6 allocs/op |
| Get existing key | 9.98–10.13 µs/op; 708 B/op; 5 allocs/op |
| Get missing key | 20.55–21.69 ns/op; 0 allocs/op |
| Delete existing key | 1.10–1.30 ms/op; 289–290 B/op; 4 allocs/op |
| Get with 1,000 keys | 10.14–10.51 µs/op |
| Get with 10,000 keys | 9.89–10.95 µs/op |
| Get with 100,000 keys | 10.67–12.26 µs/op |
| Index heap estimate, 1,000 keys | ~68.3 bytes/key |
| Index heap estimate, 10,000 keys | ~59.8 bytes/key |
| Index heap estimate, 100,000 keys | ~51.0 bytes/key |
| Open without snapshot, 1,000 keys | 13.85–14.88 ms/op |
| Open without snapshot, 10,000 keys | 129–148 ms/op |
| Open without snapshot, 100,000 keys | 1.24–1.34 s/op |
| Open with snapshot, 1,000 keys | 42.5–49.0 ms/op |
| Open with snapshot, 10,000 keys | 346–422 ms/op |
| Open with snapshot, 100,000 keys | 3.77–4.22 s/op |

GET latency was roughly stable across the tested key counts. SET and DELETE
include synchronous durability work and are not memory-only operation
measurements. The bytes-per-key values are approximate heap estimates, not
exact total memory usage. In these measurements, opening with a snapshot was
slower than opening without one at every tested size; this is a current
limitation rather than evidence that snapshots improve startup performance.

## Design documentation

See [design.md](./design.md) for the detailed public API, binary formats,
recovery rules, snapshot validation and persistence behavior, failure handling,
platform limitations, and engineering decisions.
