// Package portfwd implements the portfwd module, which diverts packets whose
// TCP or UDP source port is in a configured set to an alternative exit device.
package portfwd

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	portfwdpb "github.com/yanet-platform/yanet2/modules/portfwd/controlplane/portfwdpb/v1"
)

const (
	moduleName  = "portfwd"
	agentName   = moduleName
	serviceName = "modules.portfwd.controlplane.portfwdpb.v1.PortfwdService"
)

// Option configures the PortfwdModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the portfwd module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// PortfwdModule is a controlplane component for the portfwd module.
type PortfwdModule struct {
	cfg            *Config
	shm            *ffi.SharedMemory
	agent          *ffi.Agent
	portfwdService *PortfwdService
	log            *zap.Logger
}

// NewPortfwdModule creates a new PortfwdModule.
func NewPortfwdModule(cfg *Config, options ...Option) (*PortfwdModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	portfwdService := NewPortfwdService(NewBackend(agent))

	return &PortfwdModule{
		cfg:            cfg,
		shm:            shm,
		agent:          agent,
		portfwdService: portfwdService,
		log:            log,
	}, nil
}

// Name returns the module name.
func (m *PortfwdModule) Name() string {
	return moduleName
}

// Endpoint returns the gRPC endpoint for the portfwd module.
func (m *PortfwdModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *PortfwdModule) ServicesNames() []string {
	return []string{serviceName}
}

// RegisterService registers the portfwd module's gRPC service.
func (m *PortfwdModule) RegisterService(server *grpc.Server) {
	portfwdpb.RegisterPortfwdServiceServer(server, m.portfwdService)
}

// Close releases shared memory resources held by the module.
func (m *PortfwdModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach shared memory", zap.Error(err))
	}

	return nil
}
