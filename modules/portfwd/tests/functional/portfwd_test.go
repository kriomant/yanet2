package portfwd_test

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	blackhole "github.com/yanet-platform/yanet2/modules/blackhole/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/portfwd/bindings/go/cportfwd"
	portfwd "github.com/yanet-platform/yanet2/modules/portfwd/controlplane"
	portfwdpb "github.com/yanet-platform/yanet2/modules/portfwd/controlplane/portfwdpb/v1"
)

// Memory sizes for the portfwd functional harness.
const (
	pfCPMemory  = 64 * datasize.MB
	pfDPMemory  = 4 * datasize.MB
	pfAgentSize = 16 * datasize.MB
)

const (
	ingressDevice = "port0"
	exitDevice    = "port1"
	configName    = "test"
	// matchedPort is in the configured set, passedPort is not.
	matchedPort = 1234
	passedPort  = 4321
)

// setupHarness builds a dataplane harness carrying the portfwd module and the
// two modules the topology terminates its exits with.
func setupHarness(t *testing.T, devices []string) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(pfCPMemory),
		DPMemory:      uint64(pfDPMemory),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"portfwd", "forward", "blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("portfwd-test", 0, pfAgentSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return h, agent
}

// applyConfig publishes a portfwd config to shared memory.
//
// It must run before wireTopology, because the chain module reference is
// resolved when the devices are registered.
func applyConfig(t *testing.T, agent *ffi.Agent, settings portfwd.ModuleSettings) {
	t.Helper()

	handle, err := portfwd.NewBackend(agent).UpdateModule(configName, settings)
	require.NoError(t, err)
	t.Cleanup(handle.Free)
}

// catchAllForwardRules returns ModeOut forward rules matching every packet
// type, so pass-through packets reach the device's egress stage.
//
// An input entry point cannot transmit directly, so without this sink nothing
// that portfwd leaves in the chain output would ever surface as Output.
func catchAllForwardRules(device string) []cforward.ForwardRule {
	return []cforward.ForwardRule{
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink_l2",
			Devices: filter.Devices{{Name: device}},
		},
	}
}

// wireTopology wires the ingress device as chain[portfwd -> forward sink] and
// terminates the diverted exit on target with a blackhole.
//
// That asymmetry is what makes the module's two exits distinguishable in a
// single round: pass-through packets surface in Output through the ingress
// device's egress, diverted ones in Drop. mode selects which of the target's
// pipelines the blackhole sits on, matching where the module re-injects.
// An empty target leaves the diversion unwired.
func wireTopology(t *testing.T, agent *ffi.Agent, target string, mode cportfwd.Mode) {
	t.Helper()

	sink := configName + "-sink"
	sinkHandle, err := forward.NewBackend(agent).UpdateModule(sink, catchAllForwardRules(ingressDevice))
	require.NoError(t, err)
	t.Cleanup(sinkHandle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "portfwd", Name: configName},
					{Type: "forward", Name: sink},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))

	// A pipeline with no functions passes packets straight through. Each
	// device stage needs its own name to avoid counter-key collisions.
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "egress_" + ingressDevice}))

	devices := []ffi.DeviceConfig{{
		Name:   ingressDevice,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "egress_" + ingressDevice, Weight: 1}},
	}}

	if target != "" {
		devices = append(devices, blackholedDevice(t, agent, target, mode))
	}

	require.NoError(t, agent.UpdatePlainDevices(devices))
}

// blackholedDevice builds a device whose diverted-side pipeline drops
// everything and whose other side passes through.
func blackholedDevice(
	t *testing.T,
	agent *ffi.Agent,
	target string,
	mode cportfwd.Mode,
) ffi.DeviceConfig {
	t.Helper()

	hole := configName + "-hole"
	holeHandle, err := blackhole.NewBackend(agent).UpdateModule(hole)
	require.NoError(t, err)
	t.Cleanup(holeHandle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: hole,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    hole + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: hole}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      hole,
		Functions: []string{hole},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "spare_" + target}))

	diverted := []ffi.DevicePipelineConfig{{Name: hole, Weight: 1}}
	spare := []ffi.DevicePipelineConfig{{Name: "spare_" + target, Weight: 1}}

	if mode == cportfwd.ModeIn {
		return ffi.DeviceConfig{Name: target, Input: diverted, Output: spare}
	}

	return ffi.DeviceConfig{Name: target, Input: spare, Output: diverted}
}

func ethIPv4(proto layers.IPProtocol) (layers.Ethernet, layers.IPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: proto,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	return eth, ip4
}

func tcpPacket(t *testing.T, srcPort uint16) gopacket.Packet {
	t.Helper()

	eth, ip4 := ethIPv4(layers.IPProtocolTCP)
	tcp := layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: 80,
		SYN:     true,
	}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip4))

	return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
}

func udpPacket(t *testing.T, srcPort uint16) gopacket.Packet {
	t.Helper()

	eth, ip4 := ethIPv4(layers.IPProtocolUDP)
	udp := layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: 9000,
	}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip4))

	return xpacket.LayersToPacket(t, &eth, &ip4, &udp, gopacket.Payload([]byte("payload")))
}

func icmpPacket(t *testing.T) gopacket.Packet {
	t.Helper()

	eth, ip4 := ethIPv4(layers.IPProtocolICMPv4)
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}

	return xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
}

// fragmentPacket builds a non-initial TCP fragment whose payload starts with
// srcPort, so a missing fragment guard would read it as a source port.
func fragmentPacket(t *testing.T, srcPort uint16) gopacket.Packet {
	t.Helper()

	eth, ip4 := ethIPv4(layers.IPProtocolTCP)
	ip4.FragOffset = 8

	payload := make([]byte, 32)
	binary.BigEndian.PutUint16(payload[0:2], srcPort)

	return xpacket.LayersToPacket(t, &eth, &ip4, gopacket.Payload(payload))
}

func divertSettings(target string, mode cportfwd.Mode, ports ...uint16) portfwd.ModuleSettings {
	return portfwd.ModuleSettings{Ports: ports, Target: target, Mode: mode}
}

// TestPortfwd_SplitsTCPBySourcePort verifies the two exits differ: a TCP
// packet on a configured source port is diverted to the target device while
// one on another port continues down the chain.
func TestPortfwd_SplitsTCPBySourcePort(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})
	applyConfig(t, agent, divertSettings(exitDevice, cportfwd.ModeOut, matchedPort))
	wireTopology(t, agent, exitDevice, cportfwd.ModeOut)

	result, err := h.HandlePackets(tcpPacket(t, matchedPort), tcpPacket(t, passedPort))
	require.NoError(t, err)

	require.Len(t, result.Drop, 1, "matching packet must take the alternative exit")
	require.Equal(t, uint16(matchedPort), result.Drop[0].SrcPort)

	require.Len(t, result.Output, 1, "non-matching packet must stay in the chain")
	require.Equal(t, uint16(passedPort), result.Output[0].SrcPort)
}

// TestPortfwd_SplitsUDPBySourcePort verifies that the same port set applies to
// UDP, not only TCP.
func TestPortfwd_SplitsUDPBySourcePort(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})
	applyConfig(t, agent, divertSettings(exitDevice, cportfwd.ModeOut, matchedPort))
	wireTopology(t, agent, exitDevice, cportfwd.ModeOut)

	result, err := h.HandlePackets(udpPacket(t, matchedPort), udpPacket(t, passedPort))
	require.NoError(t, err)

	require.Len(t, result.Drop, 1, "matching packet must take the alternative exit")
	require.Equal(t, uint16(matchedPort), result.Drop[0].SrcPort)

	require.Len(t, result.Output, 1, "non-matching packet must stay in the chain")
	require.Equal(t, uint16(passedPort), result.Output[0].SrcPort)
}

// TestPortfwd_ModeIn diverts into the target's ingress pipeline rather than
// its egress one.
func TestPortfwd_ModeIn(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})
	applyConfig(t, agent, divertSettings(exitDevice, cportfwd.ModeIn, matchedPort))
	wireTopology(t, agent, exitDevice, cportfwd.ModeIn)

	result, err := h.HandlePackets(tcpPacket(t, matchedPort), tcpPacket(t, passedPort))
	require.NoError(t, err)

	require.Len(t, result.Drop, 1, "matching packet must reach the target's ingress pipeline")
	require.Equal(t, uint16(matchedPort), result.Drop[0].SrcPort)
	require.Len(t, result.Output, 1)
	require.Equal(t, uint16(passedPort), result.Output[0].SrcPort)
}

// TestPortfwd_IgnoresNonTransportTraffic verifies that ICMP and non-initial
// fragments always take the pass-through exit, even when the bytes at the
// transport offset spell a configured port.
func TestPortfwd_IgnoresNonTransportTraffic(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})
	applyConfig(t, agent, divertSettings(exitDevice, cportfwd.ModeOut, matchedPort))
	wireTopology(t, agent, exitDevice, cportfwd.ModeOut)

	result, err := h.HandlePackets(icmpPacket(t), fragmentPacket(t, matchedPort))
	require.NoError(t, err)

	require.Empty(t, result.Drop, "neither packet carries a matchable source port")
	require.Len(t, result.Output, 2)
}

// TestPortfwd_ChangedPortSetStopsDiverting is the mutation check for the
// bitmap lookup: moving the configured port off the traffic's port must send
// both packets down the pass-through exit.
func TestPortfwd_ChangedPortSetStopsDiverting(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})
	applyConfig(t, agent, divertSettings(exitDevice, cportfwd.ModeOut, matchedPort+1))
	wireTopology(t, agent, exitDevice, cportfwd.ModeOut)

	result, err := h.HandlePackets(tcpPacket(t, matchedPort), tcpPacket(t, passedPort))
	require.NoError(t, err)

	require.Empty(t, result.Drop, "no packet matches the shifted port set")
	require.Len(t, result.Output, 2)
}

// TestPortfwd_UnmappedTargetDrops verifies that a target device linked in the
// config but absent from the registered topology drops matching packets
// instead of sending them to an arbitrary port.
func TestPortfwd_UnmappedTargetDrops(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice})
	// "phantom" never appears in UpdatePlainDevices, so its mc_index slot
	// stays at the sentinel.
	applyConfig(t, agent, divertSettings("phantom", cportfwd.ModeOut, matchedPort))
	wireTopology(t, agent, "", cportfwd.ModeOut)

	result, err := h.HandlePackets(tcpPacket(t, matchedPort), tcpPacket(t, passedPort))
	require.NoError(t, err)

	require.Len(t, result.Drop, 1, "matching packet with an unmapped target must be dropped")
	require.Equal(t, uint16(matchedPort), result.Drop[0].SrcPort)
	require.Len(t, result.Output, 1)
	require.Equal(t, uint16(passedPort), result.Output[0].SrcPort)
}

// TestPortfwd_ServiceUpdateDiverts drives the same path through the gRPC
// service rather than the backend, so its validation and port narrowing run
// end to end.
func TestPortfwd_ServiceUpdateDiverts(t *testing.T) {
	h, agent := setupHarness(t, []string{ingressDevice, exitDevice})

	svc := portfwd.NewPortfwdService(portfwd.NewBackend(agent))
	_, err := svc.UpdateConfig(t.Context(), &portfwdpb.UpdateConfigRequest{
		Name:   configName,
		Ports:  []uint32{matchedPort, matchedPort},
		Target: exitDevice,
		Mode:   portfwdpb.ForwardMode_OUT,
	})
	require.NoError(t, err)

	wireTopology(t, agent, exitDevice, cportfwd.ModeOut)

	result, err := h.HandlePackets(tcpPacket(t, matchedPort), tcpPacket(t, passedPort))
	require.NoError(t, err)

	require.Len(t, result.Drop, 1)
	require.Equal(t, uint16(matchedPort), result.Drop[0].SrcPort)
	require.Len(t, result.Output, 1)
	require.Equal(t, uint16(passedPort), result.Output[0].SrcPort)
}
