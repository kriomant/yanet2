#include "dataplane.h"
#include "config.h"

#include <netinet/in.h>

#include <stdio.h>
#include <stdlib.h>

#include "common/container_of.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/pipeline/econtext.h"

// Included after data.h, which supplies the packet_to_mbuf this header calls
// without declaring.
#include <filter/query/port.h>

// Report whether the packet carries a TCP or UDP source port in the set.
//
// A non-initial fragment repeats the protocol of its first fragment but
// carries payload where the transport header would be, and packet_src_port
// reports 0 for anything that is neither TCP nor UDP. Port 0 may itself be in
// the set, so both cases are rejected before the lookup rather than after.
static inline int
portfwd_match(
	const struct portfwd_module_config *config, const struct packet *packet
) {
	if (packet->fragment_offset != 0) {
		return 0;
	}

	uint16_t proto = packet->transport_header.type;
	if (proto != IPPROTO_TCP && proto != IPPROTO_UDP) {
		return 0;
	}

	return portfwd_port_is_set(config, packet_src_port(packet));
}

static void
portfwd_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct portfwd_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct portfwd_module_config,
		cp_module
	);

	if (config->mode == PORTFWD_MODE_NONE || config->port_count == 0) {
		packet_front_pass(packet_front);
		return;
	}

	// The whole config shares one exit, so the device encoding is resolved
	// once instead of per packet.
	uint16_t device_id =
		module_ectx_encode_device(module_ectx, config->device_id);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		if (!portfwd_match(config, packet)) {
			packet_front_output(packet_front, packet);
			continue;
		}

		if (device_id == (uint16_t)-1) {
			packet_front_drop(packet_front, packet);
			continue;
		}

		packet->tx_device_id = device_id;

		if (config->mode == PORTFWD_MODE_IN) {
			packet_front_pending_input(packet_front, packet);
		} else {
			packet_front_pending_output(packet_front, packet);
		}
	}
}

struct portfwd_module {
	struct module module;
};

struct module *
new_module_portfwd() {
	struct portfwd_module *module =
		(struct portfwd_module *)malloc(sizeof(struct portfwd_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"portfwd"
	);
	module->module.handler = portfwd_handle_packets;

	return &module->module;
}
