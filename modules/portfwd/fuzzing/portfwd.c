#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#include "dataplane/config/zone.h"
#include "modules/portfwd/api/controlplane.h"
#include "modules/portfwd/dataplane/config.h"
#include "modules/portfwd/dataplane/dataplane.h"

#include "lib/fuzzing/fuzzing.h"

static struct fuzzing_params fuzz_params = {0};

static int
portfwd_test_config(struct cp_module **cp_module, yanet_error **err) {
	struct portfwd_module_config *config =
		(struct portfwd_module_config *)memory_balloc(
			&fuzz_params.mctx, sizeof(struct portfwd_module_config)
		);

	if (!config) {
		return -ENOMEM;
	}

	memset(config, 0, sizeof(struct portfwd_module_config));

	// Initialize cp_module fields.
	strtcpy(config->cp_module.name,
		"portfwd_test",
		sizeof(config->cp_module.name));
	memory_context_init_from(
		&config->cp_module.memory_context,
		&fuzz_params.mctx,
		"portfwd_test"
	);

	config->cp_module.dp_module_idx = 0;
	config->cp_module.agent = NULL;

	// Needed so the fail path below can finalize the module.
	if (counter_registry_init(
		    &config->cp_module.counter_registry,
		    &config->cp_module.memory_context,
		    0
	    )) {
		goto fail;
	}

	static const uint16_t ports[] = {0, 53, 443, 8080, 65535};

	if (portfwd_module_config_update(
		    &config->cp_module,
		    "dev1",
		    PORTFWD_MODE_OUT,
		    ports,
		    sizeof(ports) / sizeof(ports[0]),
		    err
	    )) {
		goto fail;
	}

	// Map the linked device to a valid slot so matching packets reach the
	// diversion path rather than the invalid-device drop.
	uint64_t *mc_index = memory_balloc(&fuzz_params.mctx, sizeof(uint64_t));
	if (mc_index == NULL) {
		goto fail;
	}
	mc_index[0] = 0;
	fuzz_params.module_ectx.mc_index_size = 1;
	SET_OFFSET_OF(&fuzz_params.module_ectx.mc_index, mc_index);

	*cp_module = (struct cp_module *)config;
	return 0;

fail:
	portfwd_module_config_free(&config->cp_module);
	return -EINVAL;
}

static int
fuzz_setup(yanet_error **err) {
	if (fuzzing_params_init(
		    &fuzz_params, "portfwd fuzzing", new_module_portfwd
	    ) != 0) {
		return EXIT_FAILURE;
	}

	return portfwd_test_config(&fuzz_params.cp_module, err);
}

int
LLVMFuzzerTestOneInput(const uint8_t *data, size_t size) { // NOLINT
	if (fuzz_params.module == NULL) {
		yanet_error *err = NULL;
		if (fuzz_setup(&err) != 0) {
			exit(1); // Proper setup is essential for continuing
		}
	}

	return fuzzing_process_packet(&fuzz_params, data, size);
}
