package web

import "os"

// openFSRoot opens the OS directory and returns a reader
// that satisfies the readDirCloser interface declared in
// batch_ingest_test.go. Kept in a separate file so the test
// file's imports stay focused on the test cases themselves.
func openFSRoot(dir string) (readDirCloser, error) {
	return os.OpenFile(dir, os.O_RDONLY, 0)
}

// osStat wraps os.Stat so the test file doesn't need to
// import os directly (the helpers file owns the import).
func osStat(name string) (statResult, error) {
	return os.Stat(name)
}

// statResult is the slice of *os.FileInfo the test actually
// uses — just the IsDir method. Wrapping it here keeps the
// test's dependency surface minimal.
type statResult interface {
	IsDir() bool
}
