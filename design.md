# kminv — Design Specification

**Version:** 1.0  
**Language:** Go  
**Project type:** Persistent, embedded key-value database  
**Status:** Baseline design — implementation may revise decisions when tests or experiments expose flaws.

---

## 1. Project Overview

### 1.1 What is kminv?

kminv is a small, persistent key-value database engine implemented from scratch in Go.

It accepts keys and values, stores mutations in a local binary log, and maintains an in-memory hash index to make lookups efficient. It is designed to teach the engineering fundamentals behind storage engines: binary serialization, file I/O, indexing, crash recovery, durability, synchronization, and performance measurement.

kminv is a database engine, not an application built on top of an existing database.

### 1.2 Primary goals

- Implement a persistent key-value store from first principles.
- Maintain correctness across process restarts and recoverable crashes.
- Separate authoritative storage from disposable in-memory state.
- Develop clear component boundaries and maintainable interfaces.
- Learn to reason about failure modes, data integrity, and synchronization.
- Establish a measurable performance baseline before attempting optimizations.

### 1.3 Non-goals for V1

kminv V1 will not implement:

- SQL, transactions, or multi-key atomic operations.
- A network server or distributed replication.
- Multiple processes accessing the same database simultaneously.
- A B-tree or LSM-tree storage architecture.
- Background compaction or log deletion.
- Automatic eviction or expiration of keys.
- Encryption or cryptographic integrity protection.
- A general-purpose query language.

These features may be considered later, but they must not complicate the initial implementation without a demonstrated need.

---

## 2. Core Design Principles

### 2.1 The log is authoritative

The append-only log contains the history of committed mutations. The in-memory index is derived from that history and can be reconstructed.

If the index disagrees with the log, the log takes precedence.

### 2.2 Append instead of overwrite

An update appends a new record rather than modifying an earlier record in place.

For example:

```text
SET("color", "red")     -> record at offset 12
SET("color", "blue")    -> record at offset 40
DELETE("color")         -> record at offset 81
```

The index points to the latest record for the key. Earlier records remain in the log.

### 2.3 Correctness before optimization

The initial implementation prioritizes understandable behavior, explicit failure handling, and deterministic recovery over throughput.

### 2.4 Keep abstractions proportional to the project

V1 will use a single Go package. Components will have clear responsibilities without introducing unnecessary interfaces, packages, or generic frameworks.

### 2.5 Validate at trust boundaries

Public API inputs are validated for clear errors. The record codec independently validates records before encoding and validates untrusted bytes before decoding.

The log and snapshot layers must never trust lengths, offsets, or operation codes read from disk without validation.

---

## 3. Supported Data Model

### 3.1 Keys and values

Keys and values are arbitrary byte sequences, not necessarily UTF-8 strings.

| Property | Rule |
|---|---|
| Key type | `[]byte` |
| Value type | `[]byte` |
| Minimum key length | 0 bytes |
| Maximum key length | 1,024 bytes |
| Minimum value length | 0 bytes |
| Maximum value length | 1,048,576 bytes (1 MiB) |
| Empty values | Allowed |
| Empty keys | Allowed |

Because keys may contain arbitrary bytes, the in-memory index uses `string(key)` as the map key. Go strings can contain arbitrary byte sequences, including zero bytes; they are not restricted to text.

### 3.2 Operations

The database supports three public operations:

- **Set:** Store or replace the value associated with a key.
- **Get:** Retrieve the current value associated with a key.
- **Delete:** Remove a key from the logical database.

The on-disk log stores only mutations: `SET` and `DELETE`. Reads do not create log records.

### 3.3 Operation constants

```go
const (
    OpSet    byte = 0x01
    OpDelete byte = 0x02
)
```

A record with an unknown operation code is invalid.

---

## 4. Public API

The intended API is:

```go
func Open(path string) (*DB, error)

func (db *DB) Set(key, value []byte) error

func (db *DB) Get(key []byte) ([]byte, bool, error)

func (db *DB) Delete(key []byte) error

func (db *DB) Close() error
```

### 4.1 `Open`

Opens or creates a database at the supplied path.

Responsibilities:

1. Open or create the authoritative log.
2. Validate the file header.
3. Recover the database state from valid persisted data.
4. Reconstruct or load the in-memory index.
5. Return a usable `DB` instance only after successful initialization.

If unrecoverable corruption is detected, `Open` returns an error.

### 4.2 `Set`

Stores a value for a key.

Rules:

- Validate the key and value lengths.
- Encode a `SET` record.
- Append the record to the log.
- Synchronize the log to durable storage using `File.Sync`.
- Update the in-memory index only after the append and synchronization succeed.

An empty value is a valid value and must remain distinguishable from a missing key.

### 4.3 `Get`

Returns:

- The value, `true`, and `nil` if the key exists.
- `nil`, `false`, and `nil` if the key does not exist.
- An error if the database cannot safely complete the operation.

The index provides the latest record offset. The database reads and validates that record from the log before returning its value.

The returned byte slice must be independent of internal mutable buffers.

A storage corruption error must not be treated as an ordinary cache miss or missing key.

### 4.4 `Delete`

Removes a key from the logical database.

Rules:

- If the key exists, append and synchronize a `DELETE` record, then remove it from the index.
- If the key does not exist, return success without appending a record.
- If appending or synchronizing fails, do not report a successful deletion.

### 4.5 `Close`

Closes the database safely.

It must:

1. Prevent new operations from beginning.
2. Wait for operations already in progress.
3. Attempt to persist an index snapshot if snapshot support is enabled.
4. Close the log file.
5. Return an error if required shutdown work fails.

Closing must be safe to call more than once. The implementation must define and test its exact behavior when the database is already closed or in a failed state.

---

## 5. On-Disk Log Format

The database uses a binary append-only log.

### 5.1 File header

Every log starts with a 12-byte header.

| Field | Size | Encoding |
|---|---:|---|
| Magic | 8 bytes | ASCII `MINIKVDB` |
| Version | 4 bytes | Unsigned big-endian integer; value `1` |

The magic identifies the file format. The version allows incompatible formats to be rejected explicitly.

A missing or truncated header is an initialization error. An unrecognized magic or unsupported version is not silently repaired.

### 5.2 Record layout

Each record uses this exact layout:

| Field | Size | Description |
|---|---:|---|
| Record length | 4 bytes | Big-endian unsigned integer |
| CRC-32 checksum | 4 bytes | Big-endian unsigned integer |
| Operation | 1 byte | `OpSet` or `OpDelete` |
| Key length | 4 bytes | Big-endian unsigned integer |
| Value length | 4 bytes | Big-endian unsigned integer |
| Key | Variable | Exactly `key_length` bytes |
| Value | Variable | Exactly `value_length` bytes |

All integer fields use big-endian encoding.

The record length excludes its own four-byte prefix but includes the checksum and every subsequent field.

Therefore:

\[
\text{record\_length}=13+\text{key\_length}+\text{value\_length}
\]

\[
\text{physical\_size}=17+\text{key\_length}+\text{value\_length}
\]

The maximum encoded record length is 1,049,613 bytes. The maximum physical record size is 1,049,617 bytes.

### 5.3 Checksum

The checksum uses Go's `hash/crc32` package with the IEEE CRC-32 polynomial.

It covers:

- Operation byte.
- Key length.
- Value length.
- Key bytes.
- Value bytes.

It excludes the record-length prefix and the checksum field itself.

CRC-32 detects many accidental corruption cases. It is not a cryptographic integrity guarantee and cannot repair damaged data.

### 5.4 Record invariants

Before encoding or after decoding, a record must satisfy these rules:

1. The operation is recognized.
2. The key length is at most 1,024 bytes.
3. The value length is at most 1 MiB.
4. A `DELETE` record has an empty value.
5. A `SET` record may contain an empty value.
6. Encoded lengths exactly match the corresponding byte sequences.
7. The complete record fits within the supported maximum size.

The codec must validate lengths before allocation or slicing based on decoded values.

A malformed record must never cause an unbounded allocation, integer overflow, or panic.

---

## 6. Shared Record Model

Record-related functions use one common model.

```go
type Record struct {
    Op    byte
    Key   []byte
    Value []byte
}
```

The intended codec functions are:

```go
func encodeRecord(r Record) ([]byte, error)

func decodeRecord(data []byte) (Record, error)
```

The encoder validates the model before serialization. The decoder validates the encoded representation before returning a model.

The `Record` type is not the universal data structure for the entire database. Different components may use different types when their responsibilities differ.

For example, the index stores keys and offsets, not full records. This avoids retaining every value in memory and keeps the storage and indexing responsibilities separate.

---

## 7. In-Memory Index

The index maps each key to the offset of its latest log record.

Conceptually:

```go
type Index map[string]int64
```

The offset points to the beginning of the record's four-byte length prefix.

### 7.1 Why store offsets?

Storing offsets rather than values keeps the index relatively small and makes the log the single authoritative store of values.

The cost is that a `Get` operation must read and decode the record from disk.

### 7.2 Index update rules

For a successful `SET`:

1. Append the record.
2. Synchronize the log.
3. Update the key's index entry to the new offset.

For a successful `DELETE`:

1. Append the record.
2. Synchronize the log.
3. Remove the key's index entry.

The index must never be updated before the corresponding mutation is successfully synchronized.

### 7.3 Deleted keys and historical records

A deletion removes the key from the logical index, but historical records remain in the log.

If the same key is subsequently set again, the new `SET` record becomes its current value.

### 7.4 Read validation

A `Get` operation must validate the record read at the indexed offset, including its checksum, operation and key.

If the offset points to a deletion record, a different key, or an invalid record, the database reports corruption rather than returning incorrect data.

---

## 8. Component Architecture

V1 begins with a single package named `kminv`.

The components below are logical responsibilities, not necessarily separate Go packages.

### 8.1 `db.go` — Database coordinator

Responsibilities:

- Implement the public API.
- Coordinate the log, index and codec.
- Enforce synchronization and database lifecycle rules.
- Apply mutations in the correct order.
- Coordinate startup recovery and shutdown.

Recovery coordination may live here initially rather than being abstracted into a separate subsystem.

### 8.2 `record.go` — Record codec

Responsibilities:

- Define `Record` and operation constants.
- Encode valid records.
- Decode and validate records.
- Calculate and verify checksums.
- Reject malformed or oversized records.

The codec should be deterministic and independent of filesystem state. It should be straightforward to test without creating a database file.

### 8.3 `log.go` — Log storage

Responsibilities:

- Create and validate the file header.
- Append encoded records.
- Read records by offset.
- Synchronize writes.
- Truncate to a known valid boundary when recovery requires it.
- Expose the current valid log position.

It should not decide whether a key exists or maintain the logical key index.

### 8.4 `snapshot.go` — Index snapshot persistence

Responsibilities:

- Serialize the index into a snapshot.
- Validate snapshot metadata and checksums.
- Write a new snapshot safely.
- Load a valid snapshot when the startup consistency checks permit it.

Snapshot support must never be allowed to hide corruption in the authoritative log.

### 8.5 Tests

Tests will initially live in the same package for straightforward access to internal helpers. They can be split into focused files as the implementation grows.

Suggested files:

- `record_test.go`
- `log_test.go`
- `db_test.go`
- `recovery_test.go`
- `snapshot_test.go`

---

## 9. Startup and Recovery

Recovery rebuilds the logical database state from durable storage.

### 9.1 Record replay

During replay:

- A valid `SET` makes its key point to that record's offset.
- A valid `DELETE` removes the key from the index.
- Reads are never replayed because they are not stored.
- The latest valid mutation for each key determines its state.

### 9.2 Recoverable tail damage

The following cases can be treated as an incomplete final append:

1. A partial record-length prefix at the end of the file.
2. A complete length prefix declaring a record whose bytes extend beyond EOF, provided the declared length is within valid bounds.

In these cases:

1. Preserve all records before the incomplete record.
2. Truncate the file to the last valid record boundary.
3. Synchronize the truncation.
4. Continue startup and report that tail recovery occurred.

Recovery must not silently treat arbitrary invalid bytes as a valid record.

### 9.3 Unrecoverable corruption

Startup must fail if it encounters:

- An invalid or truncated file header.
- An unsupported format version.
- An unknown operation code in a complete record.
- An invalid checksum in a complete record.
- Inconsistent or impossible length fields.
- An oversized declared record.
- Corruption within the middle of the log.

The implementation must not scan arbitrary bytes for a possible next record. Doing so could reinterpret corrupted data as valid mutations.

### 9.4 Valid boundary tracking

Recovery must track the end offset of the last fully validated record. Truncation must use that boundary, never a guessed offset.

---

## 10. Write Durability and Failure Handling

### 10.1 Mutation ordering

For every mutation that changes the database:

1. Validate the request.
2. Encode the record.
3. Remember the previous valid log end.
4. Append the complete record.
5. Call `File.Sync`.
6. Update the in-memory index.
7. Return success.

This ordering ensures that the index does not advertise a mutation before the log has passed the selected durability boundary.

A successful `File.Sync` is the application's practical durability boundary, not an absolute guarantee against every hardware, filesystem, or storage-device failure.

### 10.2 Failed writes

If writing or synchronization fails:

- Do not update the index.
- Attempt to truncate the log to the previous valid end.
- Synchronize the truncation if possible.

If rollback is confirmed, return the operation error and permit further operations.

If rollback cannot be confirmed, mark the database instance as failed. Reject subsequent operations until the instance is closed and reopened.

This prevents the process from continuing to operate on a log whose state is uncertain.

### 10.3 Failed-state behavior

A failed database instance must not silently resume normal reads and writes.

The implementation must define which operations remain permitted in the failed state, particularly `Close`, and test them.

### 10.4 File creation durability

Synchronizing the log does not necessarily guarantee that a newly created filename's directory entry is durable on every supported platform.

Directory synchronization may be needed for stronger crash-durability guarantees. This behavior must be documented and implemented according to the supported operating systems.

---

## 11. Concurrency and Lifecycle

### 11.1 V1 concurrency model

One `DB` instance supports concurrent calls from goroutines through a single mutex.

The mutex is held across relevant disk operations, including synchronization. This favors simple, understandable correctness over high throughput.

### 11.2 Single-writer assumption

Only one active database instance or process may access a database directory at a time.

V1 does not provide operating-system file locking or cross-process coordination. Concurrently opening the same database from separate processes is unsupported.

### 11.3 Close semantics

Closing must prevent new operations from starting and wait for existing operations to finish before closing the file.

The lock and lifecycle state must be designed to avoid deadlocks between `Close` and active operations.

### 11.4 Future concurrency improvements

Only after correctness is established may the implementation consider:

- Read/write locks.
- Group commit.
- Per-key synchronization.
- Background checkpointing.
- Concurrent read paths.

These are not V1 requirements.

---

## 12. Index Snapshots

A separate index snapshot file, such as `db.index`, is intended to reduce the work needed to reconstruct the in-memory index.

The log remains authoritative. A snapshot is only a derived representation of state.

### 12.1 Snapshot format

The proposed binary format is:

| Field | Size | Purpose |
|---|---:|---|
| Start magic | 8 bytes | Identifies the snapshot format |
| Version | 4 bytes | Snapshot format version |
| Payload length | 8 bytes | Size of the serialized index payload |
| Log position | 8 bytes | Exact log boundary covered by the snapshot |
| Payload | Variable | Serialized key-to-offset entries |
| CRC-32 | 4 bytes | Detects accidental snapshot corruption |
| End magic | 8 bytes | Validates the end marker |

The exact magic bytes and payload serialization must be finalized before implementing the snapshot codec.

The checksum covers the version, payload length, log position and payload. The end marker is validated separately.

### 12.2 Snapshot consistency

The snapshot's log position must be the exact end boundary of the log prefix represented by the snapshot. It must never point into the middle of a record or beyond the valid log end.

Every stored offset must be within the relevant log boundary and must refer to a valid record for the associated key.

A snapshot must not be trusted solely because its own checksum is valid. Its metadata, entries and relationship to the authoritative log must also be checked.

**Important correctness constraint:** skipping the log prefix and replaying only the suffix cannot, by itself, detect corruption in the skipped prefix. Therefore, the initial implementation must validate the required log prefix before relying on a snapshot. A future optimization that avoids this scan requires a separately designed and documented integrity strategy.

### 12.3 Snapshot loading

At startup:

1. Validate snapshot structure, version, length and checksum.
2. Validate its log position and index entries.
3. Validate the relevant authoritative log data and snapshot consistency.
4. If valid, use the snapshot and replay any valid suffix.
5. If missing or invalid, rebuild the index from the log.
6. If the authoritative log is corrupt, fail startup rather than allowing the snapshot to conceal the problem.

A snapshot must never convert unrecoverable log corruption into a successful startup.

### 12.4 Snapshot writing

Snapshots are written only during graceful shutdown in V1.

The intended procedure is:

1. Serialize a consistent index and its exact log position.
2. Write to a temporary snapshot file.
3. Synchronize the temporary file.
4. Close it and check for errors.
5. Atomically replace the published snapshot using a replacement method supported by the target platform.
6. Synchronize the parent directory where supported and required.

The old published snapshot must remain intact until the new snapshot is ready to replace it.

A failed snapshot write must not invalidate the authoritative log. Snapshot errors must be reported by `Close`.

The implementation must also ensure that the index and log position represent one consistent state.

### 12.5 Scope and trade-off

Snapshots are included in the planned architecture, but their consistency rules must be implemented and tested carefully. Because V1 validates the relevant log prefix, the initial snapshot implementation may reduce index reconstruction work without eliminating the cost of scanning the log.

A larger startup-speed improvement is a future optimization, not a guarantee of the first implementation.

---

## 13. Future Caching Design

Caching is a possible future extension, not part of V1.

The initial index stores only key-to-offset mappings. A future cache could retain values for frequently accessed keys, avoiding repeated log reads.

A cache must not become an independent source of truth.

Any implementation must address:

- Cache invalidation after successful `SET` and `DELETE`.
- Concurrent reads and writes.
- Stale values inserted by reads that overlap writes.
- Memory limits and eviction policy.
- Whether cached byte slices can be mutated by callers.
- Correctness after failed writes or recovery.

A per-key lock is not automatically required. The appropriate synchronization strategy depends on the chosen cache design.

The cache should be introduced only after the uncached implementation is correct and benchmarked. Its value should be demonstrated by measurements rather than assumed.

---

## 14. Error Handling

Errors should preserve useful context and must not be silently ignored.

Expected categories include:

- Invalid API input.
- Closed database.
- Failed database instance.
- File I/O failure.
- Record encoding or decoding failure.
- Checksum mismatch.
- Unsupported format version.
- Snapshot validation failure.
- Recoverable tail truncation.
- Unrecoverable log corruption.

The implementation may define sentinel errors or typed errors when callers need to distinguish categories programmatically. It should avoid creating a large error hierarchy before actual use cases justify it.

A missing key is not an error. A corrupt record is not a missing key.

---

## 15. Testing Strategy

Correctness is established through tests, not just successful manual runs.

### 15.1 Record codec tests

Test:

- Encoding and decoding valid `SET` and `DELETE` records.
- Empty keys and empty values.
- Binary keys and values containing zero bytes.
- Maximum permitted key and value sizes.
- Oversized inputs.
- Unknown operations.
- Invalid deletion records with nonempty values.
- Truncated records.
- Incorrect record lengths.
- Corrupted checksums.
- Malformed length fields that could otherwise trigger excessive allocation.

For valid records, verify that decoding produces the original logical record.

### 15.2 Log tests

Test:

- Header creation and validation.
- Appending and reading records.
- Correct offset calculations.
- Synchronization and truncation behavior.
- Invalid offsets.
- Incomplete final records.
- Unsupported file versions.

### 15.3 Database API tests

Test:

- Set then Get.
- Overwriting an existing key.
- Deleting an existing key.
- Deleting a nonexistent key.
- Reading a nonexistent key.
- Empty values versus missing keys.
- Binary keys and values.
- Operations after Close.
- Operations after a failed database state.

### 15.4 Persistence tests

Test:

1. Open a database.
2. Perform several mutations.
3. Close it.
4. Reopen it.
5. Verify the resulting logical state.

Include overwrites and deletions so replay is tested against realistic mutation history.

### 15.5 Recovery tests

Test:

- Partial final length prefix.
- Incomplete final record body.
- Complete record with invalid checksum.
- Corruption in the middle of the log.
- Oversized and inconsistent lengths.
- Truncated header.
- Correct truncation to the last valid boundary.

Verify that incomplete tail recovery preserves all preceding valid mutations and that unrecoverable corruption is not silently skipped.

### 15.6 Snapshot tests

Test:

- Valid snapshot creation and loading.
- Missing and corrupted snapshots.
- Unsupported snapshot versions.
- Invalid lengths and checksums.
- Invalid offsets and key-to-record mismatches.
- Snapshot positions that fall inside records.
- Crash-safe replacement behavior where testable.
- Corrupt authoritative log data that must not be hidden by a snapshot.
- Replay of mutations appended after a snapshot boundary.

### 15.7 Failure injection

Where practical, introduce testable seams around file operations to simulate:

- Partial writes.
- Write errors.
- Synchronization errors.
- Truncation failures.
- Snapshot replacement failures.

Verify that the index never advances after a failed mutation and that uncertain rollback transitions the instance into its failed state.

### 15.8 Concurrency tests

Run concurrent reads and writes against one database instance.

Use the Go race detector:

```bash
go test -race ./...
```

The tests must validate both race freedom and logical correctness. A race-free program can still return stale or incorrect values.

### 15.9 Benchmarks

Establish baselines for:

- Set throughput.
- Get throughput.
- Delete throughput.
- Average and tail operation latency where measurable.
- Memory usage as the number of keys grows.
- Startup and recovery time for different log sizes.
- Startup with and without a usable snapshot.

Performance claims must be supported by reproducible measurements.

---

## 16. Implementation Plan

Work incrementally. Every milestone should compile and have tests before the next major component is introduced.

### Milestone 1 — Record codec

- Create the Go module and package.
- Define operation constants and `Record`.
- Implement record validation.
- Implement encoding and decoding.
- Add checksum handling.
- Test malformed input and boundary cases.

**Exit criterion:** The codec correctly round-trips valid records and rejects malformed ones without panicking.

### Milestone 2 — Log lifecycle

- Implement file creation and header validation.
- Implement append, read, sync and truncate operations.
- Track valid log boundaries.
- Add log-level tests.

**Exit criterion:** Records can be safely persisted and read back by offset.

### Milestone 3 — Database API and index

- Implement `Open` and `Close`.
- Add the in-memory key-to-offset index.
- Implement `Set`, `Get` and `Delete`.
- Enforce the correct mutation ordering.
- Add API and persistence tests.

**Exit criterion:** The database works across normal close-and-reopen cycles.

### Milestone 4 — Recovery

- Replay records during startup.
- Distinguish incomplete tail records from unrecoverable corruption.
- Truncate recoverable tails.
- Handle uncertain rollback with a failed database state.
- Add recovery and failure tests.

**Exit criterion:** Restart behavior follows the documented recovery rules.

### Milestone 5 — Index snapshots

- Finalize the snapshot format.
- Implement serialization and validation.
- Implement safe snapshot replacement.
- Establish the snapshot-to-log consistency strategy.
- Test corruption and restart behavior.

**Exit criterion:** A snapshot cannot conceal log corruption or produce an incorrect logical state.

### Milestone 6 — Concurrency and hardening

- Finalize mutex and lifecycle behavior.
- Test concurrent operations and shutdown.
- Run the race detector.
- Exercise failure paths and input boundaries.

**Exit criterion:** Supported concurrent use is correct, and failures do not silently corrupt the logical state.

### Milestone 7 — Performance baseline

- Add reproducible benchmarks.
- Measure startup, lookup, mutation and memory costs.
- Record the baseline before introducing caching or other optimizations.

**Exit criterion:** Future optimizations can be evaluated against measured behavior.

---

## 17. Initial Repository Structure

Start with a single package and a small number of files:

```text
kminv/
├── go.mod
├── db.go
├── record.go
├── log.go
├── snapshot.go
├── db_test.go
├── record_test.go
├── log_test.go
├── recovery_test.go
└── snapshot_test.go
```

The snapshot files may be introduced at Milestone 5 rather than created immediately.

Keep the implementation in one package initially. Split packages only when there is a concrete benefit to the separation.

---

## 18. Design Risks and Trade-offs

### Append-only growth

Every overwrite creates another record. The log grows even when the logical database contains few keys.

**Decision:** Accept this in V1. Measure the impact before designing compaction.

### Disk read per lookup

The index stores offsets rather than values, so a `Get` requires a log read.

**Decision:** Keep the index small and introduce caching only if benchmarks justify it.

### Synchronous writes

Calling `File.Sync` for each mutation can limit throughput.

**Decision:** Favor a clear durability boundary. Group commit can be evaluated later.

### Single mutex

Holding one mutex across disk operations limits concurrent throughput.

**Decision:** Favor simple synchronization until correctness is well tested.

### Snapshot consistency

A snapshot cannot prove that an unexamined log prefix is intact.

**Decision:** Validate the relevant authoritative log data before trusting a snapshot. Any later shortcut needs an explicit integrity design.

### Platform-dependent durability

Atomic replacement and directory synchronization differ across operating systems and filesystems.

**Decision:** Document the supported environment and avoid claiming guarantees that the implementation cannot provide.

---

## 19. Definition of Done for V1

kminv V1 is complete when:

- [ ] The public API supports Set, Get and Delete.
- [ ] Data persists across normal restarts.
- [ ] The binary format is explicitly defined and validated.
- [ ] The in-memory index stores only key-to-offset mappings.
- [ ] Empty values are distinct from missing keys.
- [ ] Recoverable incomplete tails are truncated safely.
- [ ] Unrecoverable corruption causes startup to fail.
- [ ] Failed writes do not advance the index.
- [ ] Uncertain rollback causes the instance to reject further operations.
- [ ] Concurrent use of a single instance is synchronized.
- [ ] Snapshot consistency and corruption behavior are tested.
- [ ] The test suite and race detector pass.
- [ ] Benchmarks provide a reproducible performance baseline.
- [ ] Known limitations and durability assumptions are documented.

---

## 20. Final Design Summary

kminv V1 is an embedded, single-process key-value database built around an append-only binary log and an in-memory hash index.

The log is authoritative. The index maps keys to record offsets. A shared `Record` model is used for record encoding and decoding, while each component retains its own responsibility-specific data structures. Mutations are appended and synchronized before the index changes. Recovery repairs only clearly incomplete final records and rejects unrecoverable corruption. Snapshot support must preserve the same integrity guarantees as log-based recovery.

The implementation proceeds from the record codec to log storage, API and index, recovery, snapshots, concurrency hardening, and benchmarks.
