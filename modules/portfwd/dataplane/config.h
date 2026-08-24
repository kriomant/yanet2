#pragma once

#include <stdint.h>

#include "controlplane/config/cp_module.h"

#define PORTFWD_MODE_NONE 0
#define PORTFWD_MODE_IN 1
#define PORTFWD_MODE_OUT 2

// One bit per source port value, so a lookup costs a single word load.
#define PORTFWD_PORT_WORDS (65536 / 64)

struct portfwd_module_config {
	struct cp_module cp_module;

	// Alternative exit device, as an index into the module device table
	// filled by cp_module_link_device.
	uint64_t device_id;
	// Number of ports in the set.
	uint64_t port_count;

	uint8_t mode;

	uint64_t ports[PORTFWD_PORT_WORDS];
};

static inline int
portfwd_port_is_set(const struct portfwd_module_config *config, uint16_t port) {
	return (config->ports[port >> 6] >> (port & 63)) & 1;
}

static inline void
portfwd_port_set(struct portfwd_module_config *config, uint16_t port) {
	config->ports[port >> 6] |= (uint64_t)1 << (port & 63);
}
