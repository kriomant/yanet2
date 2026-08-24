package portfwd

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/portfwd/bindings/go/cportfwd"
)

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateModule(name string, cfg ModuleSettings) (ModuleHandle, error) {
	mod, err := cportfwd.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := mod.Update(cfg.Target, cfg.Mode, cfg.Ports); err != nil {
		mod.Free()
		return nil, fmt.Errorf("failed to fill module config %q: %w", name, err)
	}

	if err := m.agent.UpdateModules(
		[]ffi.ModuleConfig{mod.AsFFIModule()},
	); err != nil {
		mod.Free()
		return nil, fmt.Errorf("failed to update module %q: %w", name, err)
	}

	return mod, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleName, name)
}
