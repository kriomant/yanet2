proto_dir = join_paths(meson.current_source_dir(), 'v1')
proto_files = [join_paths(proto_dir, 'portfwd.proto')]

protoc_gen = custom_target(
  'portfwd-protoc',
  output: ['portfwd.pb.go', 'portfwd_grpc.pb.go'],
  input: proto_files,
  command: [
    protoc,
    '-I', proto_dir,
    '--go_out=paths=source_relative:' + proto_dir,
    '--go-grpc_out=paths=source_relative:' + proto_dir,
    '@INPUT@',
  ],
  build_by_default: true,
)
portfwd_protoc_gen = protoc_gen
