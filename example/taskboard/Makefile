GOOGLEAPIS_DIR := $(shell find $$(go env GOMODCACHE) -path "*/grpc-ecosystem/grpc-gateway@*/third_party/googleapis" -type d | head -1)
PLUGIN_BIN     := ../../bin/protoc-gen-protobridge
PROTO_FILE     := proto/taskboard/v1/taskboard.proto

.PHONY: all plugin proto-go proto-bridge server clean

# Build everything: plugin → gRPC stubs → REST proxy
all: plugin proto-go proto-bridge

# 1. Build the protoc plugin
plugin:
	go build -o $(PLUGIN_BIN) ../../cmd/protoc-gen-protobridge/

# 2. Generate Go + gRPC stubs (standard protoc-gen-go + protoc-gen-go-grpc)
proto-go:
	protoc \
		--go_out=gen/grpc --go_opt=paths=source_relative \
		--go-grpc_out=gen/grpc --go-grpc_opt=paths=source_relative \
		-I proto -I ../../proto -I "$(GOOGLEAPIS_DIR)" \
		$(PROTO_FILE)

# 3. Generate REST proxy via protoc-gen-protobridge
proto-bridge: plugin
	PATH="../../bin:$$PATH" protoc \
		--protobridge_out=gen/protobridge \
		--protobridge_opt=handler_pkg=github.com/mrs1lentcz/protobridge/example/taskboard/gen/protobridge/handler \
		-I proto -I ../../proto -I "$(GOOGLEAPIS_DIR)" \
		$(PROTO_FILE)

# Run the gRPC backend server
server:
	go run ./server/

clean:
	rm -rf gen/ ../../bin/protoc-gen-protobridge
