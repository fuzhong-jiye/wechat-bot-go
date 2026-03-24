# WeChat Bot Go SDK Implementation Plan

> Status date: 2026-03-24
> Spec: `docs/superpowers/specs/2026-03-24-wechat-sdk-implementation-design.md`
> Package: `wechat`

## Outcome

The SDK is largely implemented in the repository already. The original document was a task scaffold with unchecked steps and embedded code blocks; this file now records the implementation state that exists in the repo, what was verified locally, and what remains to close before calling the SDK complete.

Implemented code is present in:

| File | Status | Notes |
|------|--------|-------|
| `errors.go` | done | Sentinel errors and `APIError` exist |
| `crypto.go` | done | AES-ECB encrypt/decrypt and PKCS7 helpers exist |
| `crypto_test.go` | done | Round-trip, padding, invalid key tests exist |
| `client.go` | done | Wire structs, auth, polling/send/upload/CDN helpers exist |
| `client_test.go` | partial verification | Tests exist, but local sandbox cannot run `httptest` listeners |
| `storage.go` | done | `Storage`, `Session`, `MemoryStorage` exist |
| `storage_test.go` | done | Save/load and isolation tests exist |
| `sqlite.go` | done | SQLite-backed storage exists |
| `sqlite_test.go` | done | Basic persistence tests exist |
| `message.go` | done | Public message/item types and parsing exist |
| `bot.go` | implemented, not committed | Bot lifecycle and send helpers exist; file is currently untracked |
| `bot_test.go` | missing | Planned but not present |
| `example_test.go` | missing | Planned but not present |

## Verified State

The following commands were run successfully in this workspace:

```bash
GOCACHE=/tmp/go-build go test ./... -run 'TestAesECB|TestMemoryStorage|TestSQLiteStorage'
```

Result: pass.

Full test execution was attempted with:

```bash
GOCACHE=/tmp/go-build go test ./...
```

That run fails in this sandbox because `httptest.NewServer` cannot bind a local port (`listen tcp6 [::1]:0: bind: operation not permitted`). This affects the HTTP client tests rather than package compilation.

## Implementation Summary

### 1. Errors

Completed in `errors.go`:

- `ErrSessionExpired`
- `ErrNoContextToken`
- `ErrContextTokenExpired`
- `APIError` with formatted error output

### 2. Crypto

Completed in `crypto.go` and `crypto_test.go`:

- AES block-by-block ECB encryption/decryption using the standard library cipher
- PKCS7 padding and unpadding
- `aesECBPaddedSize`
- Tests for round-trip behavior, padded lengths, and invalid key sizes

### 3. HTTP Client

Completed in `client.go`:

- Shared constants for API/CDN endpoints and timeouts
- Unexported wire-format structs
- Header setup for authenticated API requests
- Generic `do()` request helper with JSON marshal/unmarshal and `ret` / `errcode` handling
- API helpers for QR login, polling, sending, upload URL retrieval, CDN upload, and CDN download
- CDN URL builders

Covered by `client_test.go`:

- CDN URL builders
- auth header behavior
- no-auth behavior
- API error handling
- HTTP status error handling
- response decoding

### 4. Storage

Completed in `storage.go`, `sqlite.go`, `storage_test.go`, and `sqlite_test.go`:

- `Storage` interface
- `Session` persistence shape
- in-memory storage
- SQLite storage with schema creation
- save/load/update coverage
- persistence across reopened SQLite connections

### 5. Message Types

Completed in `message.go`:

- `Message`
- `Item`
- `ItemType`
- `TextItem`, `ImageItem`, `VoiceItem`, `FileItem`, `VideoItem`
- `Message.Text()`
- lazy media download closures
- `parseMessage`

### 6. Bot Orchestration

Implemented in `bot.go`:

- option-based configuration
- QR login flow
- session load/save behavior
- polling loop
- inbound message callback registration
- text send
- media send for image, voice, file, and video
- stop handling

This file is present but currently untracked in git.

## Remaining Work

- Add `bot_test.go` covering login, polling, send flows, and stop behavior against a mock server.
- Add `example_test.go` with a compilable example matching the design doc.
- Run the full test suite in an environment that allows loopback listeners.
- Review and reconcile implementation details that currently differ from the design doc.

## Design Mismatches To Resolve

The repo is close to the design, but it is not an exact match. These differences should be resolved before final release:

1. Upload media type constants in `client.go` are `1..4`, while the design doc specifies image/voice/file/video as `2/3/4/5`.
2. `sendMedia` sends `wireUploadURLRequest.AESKey` as raw hex, while the design doc says `base64(hex(key))`.
3. `bot.go` exists and is implemented, but the planned bot test file does not exist yet.
4. The plan originally expected `example_test.go`, which is still missing.

## Recommended Close-Out Sequence

- [x] Core package layers implemented
- [x] Crypto and storage tests passing locally
- [ ] Add missing bot tests
- [ ] Add example test
- [ ] Reconcile wire-format mismatches with the design and TS reference
- [ ] Re-run `go test ./...` outside the restricted sandbox
- [ ] Commit tracked implementation files, including `bot.go`
