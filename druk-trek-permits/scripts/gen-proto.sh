#!/usr/bin/env sh
# -----------------------------------------------------------------------------
# Regenerates Go code from the .proto files into ./gen.
# The generated code is already committed, so you ONLY need to run this if you
# change a .proto file.
#
# Requirements:
#   protoc                     (https://github.com/protocolbuffers/protobuf/releases)
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
# -----------------------------------------------------------------------------
set -e
cd "$(dirname "$0")/.."

mkdir -p gen/routepb gen/permitpb

protoc -I proto/route \
  --go_out=gen/routepb --go_opt=paths=source_relative \
  --go-grpc_out=gen/routepb --go-grpc_opt=paths=source_relative \
  proto/route/route.proto

protoc -I proto/permit \
  --go_out=gen/permitpb --go_opt=paths=source_relative \
  --go-grpc_out=gen/permitpb --go-grpc_opt=paths=source_relative \
  proto/permit/permit.proto

echo "Protobuf code generated in ./gen"
