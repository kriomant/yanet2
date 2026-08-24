# Register portfwd fuzzing target
fuzzing_targets += {
  'portfwd': {
    'source': files('portfwd.c'),
    'deps': [
      lib_packet_dp_dep,
      lib_portfwd_dp_dep,
      lib_portfwd_cp_dep,
      lib_config_dp_dep,
      lib_lib_utils_dep,
    ],
    'module_dir': '..',
  }
}
