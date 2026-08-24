package portfwd

import (
	"context"
	"math"
	"slices"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/portfwd/bindings/go/cportfwd"
	portfwdpb "github.com/yanet-platform/yanet2/modules/portfwd/controlplane/portfwdpb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
}

// ModuleSettings is the validated payload of a single portfwd configuration.
type ModuleSettings struct {
	// Ports are the TCP and UDP source ports taking the alternative exit.
	Ports []uint16
	// Target is the name of the device diverted packets are sent to.
	Target string
	Mode   cportfwd.Mode
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, applies the settings, and
	// publishes it to the dataplane.
	UpdateModule(name string, cfg ModuleSettings) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type config struct {
	Settings ModuleSettings
	Module   ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *config) Free() {
	if m.Module != nil {
		m.Module.Free()
	}
}

// PortfwdService implements the PortfwdService gRPC server.
type PortfwdService struct {
	portfwdpb.UnimplementedPortfwdServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]*config
}

// NewPortfwdService constructs a PortfwdService backed by the given Backend.
func NewPortfwdService(backend Backend) *PortfwdService {
	return &PortfwdService{
		backend: backend,
		configs: map[string]*config{},
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *PortfwdService) ListConfigs(
	ctx context.Context,
	req *portfwdpb.ListConfigsRequest,
) (*portfwdpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}

	return &portfwdpb.ListConfigsResponse{Configs: names}, nil
}

// ShowConfig returns the named config when it exists.
func (m *PortfwdService) ShowConfig(
	ctx context.Context,
	req *portfwdpb.ShowConfigRequest,
) (*portfwdpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	ports := make([]uint32, 0, len(entry.Settings.Ports))
	for _, port := range entry.Settings.Ports {
		ports = append(ports, uint32(port))
	}

	return &portfwdpb.ShowConfigResponse{
		Config: &portfwdpb.Config{
			Name:   name,
			Ports:  ports,
			Target: entry.Settings.Target,
			Mode:   modeToProto(entry.Settings.Mode),
		},
	}, nil
}

// UpdateConfig creates or replaces the named config and publishes it to the
// dataplane.
func (m *PortfwdService) UpdateConfig(
	ctx context.Context,
	req *portfwdpb.UpdateConfigRequest,
) (*portfwdpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	settings, err := settingsFromRequest(req)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.updateConfig(name, settings); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to update module config %q: %v", name, err,
		)
	}

	return &portfwdpb.UpdateConfigResponse{}, nil
}

// updateConfig publishes a fresh config and, on success, frees the old module
// handle and stores the new one.
//
// The caller must hold m.mu.
func (m *PortfwdService) updateConfig(name string, settings ModuleSettings) error {
	mod, err := m.backend.UpdateModule(name, settings)
	if err != nil {
		return err
	}

	if old, ok := m.configs[name]; ok {
		old.Free()
	}

	m.configs[name] = &config{Settings: settings, Module: mod}

	return nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *PortfwdService) DeleteConfig(
	ctx context.Context,
	req *portfwdpb.DeleteConfigRequest,
) (*portfwdpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to delete module config %q: %v", name, err,
		)
	}

	entry.Free()

	delete(m.configs, name)

	return &portfwdpb.DeleteConfigResponse{Deleted: true}, nil
}

// settingsFromRequest validates an UpdateConfig payload and narrows it to the
// types the dataplane config uses.
//
// Ports are deduplicated and sorted here rather than in the C API, so that
// ShowConfig reports the set the dataplane actually holds.
func settingsFromRequest(req *portfwdpb.UpdateConfigRequest) (ModuleSettings, error) {
	target := req.GetTarget()
	if target == "" {
		return ModuleSettings{}, status.Error(codes.InvalidArgument, "target device is required")
	}

	mode, err := modeFromProto(req.GetMode())
	if err != nil {
		return ModuleSettings{}, err
	}

	if len(req.GetPorts()) == 0 {
		return ModuleSettings{}, status.Error(codes.InvalidArgument, "at least one port is required")
	}

	ports := make([]uint16, 0, len(req.GetPorts()))
	for _, port := range req.GetPorts() {
		if port > math.MaxUint16 {
			return ModuleSettings{}, status.Errorf(
				codes.InvalidArgument,
				"port %d is out of range", port,
			)
		}

		ports = append(ports, uint16(port))
	}

	slices.Sort(ports)
	ports = slices.Compact(ports)

	return ModuleSettings{Ports: ports, Target: target, Mode: mode}, nil
}

func modeFromProto(mode portfwdpb.ForwardMode) (cportfwd.Mode, error) {
	switch mode {
	case portfwdpb.ForwardMode_IN:
		return cportfwd.ModeIn, nil
	case portfwdpb.ForwardMode_OUT:
		return cportfwd.ModeOut, nil
	default:
		return cportfwd.ModeNone, status.Error(
			codes.InvalidArgument,
			"forwarding mode must be either IN or OUT",
		)
	}
}

func modeToProto(mode cportfwd.Mode) portfwdpb.ForwardMode {
	switch mode {
	case cportfwd.ModeIn:
		return portfwdpb.ForwardMode_IN
	case cportfwd.ModeOut:
		return portfwdpb.ForwardMode_OUT
	default:
		return portfwdpb.ForwardMode_NONE
	}
}
