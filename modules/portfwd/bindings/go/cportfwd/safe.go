package cportfwd

//#include <stdlib.h>
//
//#include "modules/portfwd/api/controlplane.h"
import "C"

import (
	"runtime"
	"unsafe"
)

// Mode selects which pipeline of the target device diverted packets re-enter.
type Mode int

const (
	ModeNone Mode = 0
	ModeIn   Mode = 1
	ModeOut  Mode = 2
)

func (m Mode) toC() C.uint8_t {
	switch m {
	case ModeIn:
		return C.PORTFWD_MODE_IN
	case ModeOut:
		return C.PORTFWD_MODE_OUT
	default:
		return C.PORTFWD_MODE_NONE
	}
}

// Update replaces the port set and the alternative exit of the configuration.
//
// target names the device matching packets are diverted to. ports lists the
// TCP and UDP source ports taking that exit; duplicates collapse.
func (m *ModuleConfig) Update(target string, mode Mode, ports []uint16) error {
	cTarget := C.CString(target)
	defer C.free(unsafe.Pointer(cTarget))

	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	var cPorts *C.uint16_t
	if len(ports) > 0 {
		pinner.Pin(&ports[0])
		cPorts = (*C.uint16_t)(unsafe.Pointer(&ports[0]))
	}

	return m.update(cTarget, mode.toC(), cPorts, C.uint32_t(len(ports)))
}
