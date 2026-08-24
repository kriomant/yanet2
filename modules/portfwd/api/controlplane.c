#include "controlplane.h"

#include <string.h>

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_module.h"

struct cp_module *
portfwd_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct portfwd_module_config *config =
		(struct portfwd_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct portfwd_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "portfwd", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct portfwd_module_config)
		);
		return NULL;
	}

	config->device_id = 0;
	config->port_count = 0;
	config->mode = PORTFWD_MODE_NONE;
	memset(config->ports, 0, sizeof(config->ports));

	return &config->cp_module;
}

void
portfwd_module_config_free(struct cp_module *cp_module) {
	struct portfwd_module_config *config = container_of(
		cp_module, struct portfwd_module_config, cp_module
	);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct portfwd_module_config)
	);
}

int
portfwd_module_config_update(
	struct cp_module *cp_module,
	const char *target,
	uint8_t mode,
	const uint16_t *ports,
	uint32_t port_count,
	yanet_error **err
) {
	struct portfwd_module_config *config = container_of(
		cp_module, struct portfwd_module_config, cp_module
	);

	if (mode != PORTFWD_MODE_IN && mode != PORTFWD_MODE_OUT) {
		yanet_error_add(err, "invalid forwarding mode %u", mode);
		return -1;
	}

	if (cp_module_link_device(cp_module, target, &config->device_id, err)) {
		yanet_error_add(err, "failed to link device '%s'", target);
		return -1;
	}

	memset(config->ports, 0, sizeof(config->ports));

	uint64_t count = 0;
	for (uint32_t idx = 0; idx < port_count; ++idx) {
		if (portfwd_port_is_set(config, ports[idx])) {
			continue;
		}

		portfwd_port_set(config, ports[idx]);
		++count;
	}

	config->port_count = count;
	config->mode = mode;

	return 0;
}
