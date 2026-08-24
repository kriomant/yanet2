// Package cportfwd is the Go binding for the portfwd module.
package cportfwd

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/portfwd/api -lportfwd_cp
//
//#include <stdlib.h>
//
//#include "api/agent.h"
//#include "modules/portfwd/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the portfwd module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new portfwd module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.portfwd_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.portfwd_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// update maps 1:1 to portfwd_module_config_update.
func (m *ModuleConfig) update(
	target *C.char,
	mode C.uint8_t,
	ports *C.uint16_t,
	portCount C.uint32_t,
) error {
	var cErr *C.yanet_error
	rc := C.portfwd_module_config_update(m.asRawPtr(), target, mode, ports, portCount, &cErr)
	if rc != 0 {
		return fmt.Errorf("failed to update portfwd config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}
