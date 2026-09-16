//go:build darwin && cgo

package scan

/*
#include <sys/attr.h>
#include <sys/unistd.h>
*/
import "C"

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

// getattrlistbulk calls the supported libSystem wrapper instead of relying on
// Darwin syscall numbers, which are not a stable application interface.
func getattrlistbulk(fd int, attributes *unix.Attrlist, buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, errors.New("getattrlistbulk buffer is empty")
	}
	count, err := C.getattrlistbulk(
		C.int(fd),
		unsafe.Pointer(attributes),
		unsafe.Pointer(&buffer[0]),
		C.size_t(len(buffer)),
		C.uint64_t(fsoptAttrCommonExtended),
	)
	if count < 0 {
		return 0, err
	}
	return int(count), nil
}
