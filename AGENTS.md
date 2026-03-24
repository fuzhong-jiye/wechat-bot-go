# Repository Guidelines

## Project Structure & Module Organization

This repository is a small Go module for a WeChat bot SDK. Core library code lives at the repository root in `package wechat` (`bot.go`, `client.go`, `message.go`, `storage.go`, `sqlite.go`, `logger.go`). Tests sit beside the implementation as `*_test.go`. The runnable example is in `cmd/example/main.go`. Design notes and implementation plans are under `docs/`, including `docs/design.md` and `docs/superpowers/`.

## Build, Test, and Development Commands

- `go test ./...`: run the full test suite for the library and example module.
- `go test -run TestBot ./...`: run a focused subset while iterating on one area.
- `go build ./...`: verify all packages compile.
- `go run ./cmd/example`: start the example bot locally.
- `gofmt -w *.go cmd/example/*.go`: format changed Go files before committing.

If the sandbox blocks the default Go build cache, use `GOCACHE=/tmp/go-build go test ./...`.

## Coding Style & Naming Conventions

Follow standard Go formatting and idioms: tabs for indentation, `gofmt` output as the source of truth, and concise exported names with GoDoc-style comments when a symbol is part of the public SDK. Keep package structure flat unless a new subpackage is clearly justified. Use `CamelCase` for exported identifiers, `camelCase` for internal helpers, and table-driven tests where they improve coverage.

## Testing Guidelines

Use the standard `testing` package. Add tests in the same directory as the code they cover, named `*_test.go`, and prefer `TestXxx` names that match the public behavior under test. Cover both happy paths and error handling, especially around session state, polling, crypto helpers, and SQLite-backed storage.

## Commit & Pull Request Guidelines

Recent history uses conventional prefixes such as `feat:` and `docs:`; keep that pattern and write commits in the imperative mood. Pull requests should include a short description of the behavior change, note any API or storage impact, and list the verification steps you ran (for example, `go test ./...`). Include logs or screenshots only when changing example behavior or developer-facing output.

## Security & Configuration Tips

Do not commit live session data or local databases such as `bot.db`. Treat tokens, QR login flows, and persisted session state as sensitive. Keep example configuration local and avoid embedding credentials in source or docs.
