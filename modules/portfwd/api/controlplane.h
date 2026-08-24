#pragma once

#include <stdint.h>

#include "lib/errors/errors.h"

#define PORTFWD_MODE_NONE 0
#define PORTFWD_MODE_IN 1
#define PORTFWD_MODE_OUT 2

struct agent;
struct cp_module;

// Create a new configuration for the portfwd module.
struct cp_module *
portfwd_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
);

void
portfwd_module_config_free(struct cp_module *cp_module);

// Replace the port set and the alternative exit of the configuration.
//
// target names the device that matching packets are diverted to and mode
// picks which of its pipelines they re-enter. ports lists the TCP and UDP
// source ports taking that exit; duplicates collapse.
int
portfwd_module_config_update(
	struct cp_module *cp_module,
	const char *target,
	uint8_t mode,
	const uint16_t *ports,
	uint32_t port_count,
	yanet_error **err
);
