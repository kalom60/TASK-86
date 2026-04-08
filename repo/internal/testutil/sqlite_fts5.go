//go:build fts5 || sqlite_fts5

package testutil

// This file is intentionally empty. Its sole purpose is to pull in the
// go-sqlite3 build tag that enables FTS5 support when running tests with:
//
//	go test -tags fts5 ./...
//
// The actual driver registration is in db.go via the blank import.
