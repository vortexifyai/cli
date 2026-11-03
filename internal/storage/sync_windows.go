//go:build windows

package storage

// Windows recovery covers process interruption; directory fsync is not portable.
func SyncDirectory(path string) error { return nil }
