//go:build darwin
// +build darwin

// https://ops.tips/blog/macos-pid-absolute-path-and-procfs-exploration/#a-golang-binary-that-suits-linux-and-macos

package shell

// #include <libproc.h>
// #include <stdlib.h>
// #include <errno.h>
import "C"

import (
	"fmt"
	"unsafe"
)

// bufSize references the constant that the implementation
// of proc_pidpath uses under the hood to make sure that
// no overflows happen.
//
// See https://opensource.apple.com/source/xnu/xnu-2782.40.9/libsyscall/wrappers/libproc/libproc.c
const bufSize = C.PROC_PIDPATHINFO_MAXSIZE

func getExePathFromPid(pid int) (path string, err error) {
	// Allocate in the C heap a string (char* terminated
	// with `/0`) of size `bufSize` and then make sure
	// that we free that memory that gets allocated
	// in C (see the `defer` below).
	buf := C.CString(string(make([]byte, bufSize)))
	defer C.free(unsafe.Pointer(buf))

	// Call the C function `proc_pidpath` from the included
	// header file (libproc.h).
	ret, err := C.proc_pidpath(C.int(pid), unsafe.Pointer(buf), bufSize)
	if ret <= 0 {
		err = fmt.Errorf("failed to retrieve pid path: %v", err)
		return
	}

	// Convert the C string back to a Go string.
	path = C.GoString(buf)
	return
}
