//go:build darwin && !cgo

package scan

import "golang.org/x/sys/unix"

// getattrlistbulk reports the fast path as unavailable when cgo is disabled.
// The scanner then uses its descriptor-relative readdir/fstatat fallback.
func getattrlistbulk(_ int, _ *unix.Attrlist, _ []byte) (int, error) {
	return 0, unix.ENOSYS
}
