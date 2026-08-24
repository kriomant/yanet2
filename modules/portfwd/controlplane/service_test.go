package portfwd_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/portfwd/bindings/go/cportfwd"
	portfwd "github.com/yanet-platform/yanet2/modules/portfwd/controlplane"
	portfwdpb "github.com/yanet-platform/yanet2/modules/portfwd/controlplane/portfwdpb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

// mockBackend records the settings of the most recent UpdateModule call.
type mockBackend struct {
	last portfwd.ModuleSettings
}

func (m *mockBackend) UpdateModule(name string, cfg portfwd.ModuleSettings) (portfwd.ModuleHandle, error) {
	m.last = cfg
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

// flakyBackend succeeds on the first UpdateModule call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(name string, cfg portfwd.ModuleSettings) (portfwd.ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

func validRequest(name string) *portfwdpb.UpdateConfigRequest {
	return &portfwdpb.UpdateConfigRequest{
		Name:   name,
		Ports:  []uint32{443, 80},
		Target: "dev1",
		Mode:   portfwdpb.ForwardMode_OUT,
	}
}

func Test_PortfwdService_UpdateAndShow(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, validRequest("portfwd0"))
	require.NoError(t, err)

	show, err := svc.ShowConfig(ctx, &portfwdpb.ShowConfigRequest{Name: "portfwd0"})
	require.NoError(t, err)
	require.NotNil(t, show.GetConfig())

	assert.Equal(t, "portfwd0", show.GetConfig().GetName())
	assert.Equal(t, "dev1", show.GetConfig().GetTarget())
	assert.Equal(t, portfwdpb.ForwardMode_OUT, show.GetConfig().GetMode())
	// Sorted, so the reported set matches what the dataplane bitmap holds.
	assert.Equal(t, []uint32{80, 443}, show.GetConfig().GetPorts())
}

func Test_PortfwdService_UpdateDeduplicatesPorts(t *testing.T) {
	be := &mockBackend{}
	svc := portfwd.NewPortfwdService(be)

	req := validRequest("portfwd0")
	req.Ports = []uint32{443, 80, 443, 80, 443}

	_, err := svc.UpdateConfig(t.Context(), req)
	require.NoError(t, err)

	assert.Equal(t, []uint16{80, 443}, be.last.Ports)
	assert.Equal(t, cportfwd.ModeOut, be.last.Mode)
}

func Test_PortfwdService_UpdateRejectsInvalidPayload(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})
	ctx := t.Context()

	testCases := map[string]func(*portfwdpb.UpdateConfigRequest){
		"empty name":      func(r *portfwdpb.UpdateConfigRequest) { r.Name = "" },
		"empty target":    func(r *portfwdpb.UpdateConfigRequest) { r.Target = "" },
		"mode none":       func(r *portfwdpb.UpdateConfigRequest) { r.Mode = portfwdpb.ForwardMode_NONE },
		"no ports":        func(r *portfwdpb.UpdateConfigRequest) { r.Ports = nil },
		"port over range": func(r *portfwdpb.UpdateConfigRequest) { r.Ports = []uint32{65536} },
	}

	for name, mutate := range testCases {
		t.Run(name, func(t *testing.T) {
			req := validRequest("portfwd0")
			mutate(req)

			resp, err := svc.UpdateConfig(ctx, req)
			require.Nil(t, resp)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func Test_PortfwdService_UpdateAcceptsPortBounds(t *testing.T) {
	be := &mockBackend{}
	svc := portfwd.NewPortfwdService(be)

	req := validRequest("portfwd0")
	req.Ports = []uint32{65535, 0}

	_, err := svc.UpdateConfig(t.Context(), req)
	require.NoError(t, err)

	assert.Equal(t, []uint16{0, 65535}, be.last.Ports)
}

func Test_PortfwdService_ListUpdateList(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &portfwdpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.GetConfigs())

	_, err = svc.UpdateConfig(ctx, validRequest("portfwd0"))
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &portfwdpb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"portfwd0"}, list.GetConfigs())
}

func Test_PortfwdService_DeleteConfig(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, validRequest("portfwd0"))
	require.NoError(t, err)

	resp, err := svc.DeleteConfig(ctx, &portfwdpb.DeleteConfigRequest{Name: "portfwd0"})
	require.NoError(t, err)
	require.True(t, resp.GetDeleted())

	_, err = svc.ShowConfig(ctx, &portfwdpb.ShowConfigRequest{Name: "portfwd0"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_PortfwdService_DeleteMissing(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})

	resp, err := svc.DeleteConfig(t.Context(), &portfwdpb.DeleteConfigRequest{Name: "absent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_PortfwdService_EmptyConfigName(t *testing.T) {
	svc := portfwd.NewPortfwdService(&mockBackend{})
	ctx := t.Context()

	t.Run("ShowConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(ctx, &portfwdpb.ShowConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteConfig", func(t *testing.T) {
		resp, err := svc.DeleteConfig(ctx, &portfwdpb.DeleteConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func Test_PortfwdService_UpdateFailureKeepsPreviousConfig(t *testing.T) {
	svc := portfwd.NewPortfwdService(&flakyBackend{})
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, validRequest("portfwd0"))
	require.NoError(t, err)

	failing := validRequest("portfwd0")
	failing.Target = "dev2"

	_, err = svc.UpdateConfig(ctx, failing)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &portfwdpb.ShowConfigRequest{Name: "portfwd0"})
	require.NoError(t, err)
	assert.Equal(t, "dev1", show.GetConfig().GetTarget())
}
