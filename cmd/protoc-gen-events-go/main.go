// Command protoc-gen-events-go is the protoc plugin that generates typed
// Go publishers and subscribers for proto messages annotated with
// (protobridge.event), plus an AsyncAPI 3.0 schema describing the contract.
//
// Usage:
//
//	protoc \
//	    --events-go_out=./gen \
//	    -I . -I path/to/protobridge/proto \
//	    your/events.proto
//
// The plugin emits one `<pkg>_events.go` file per Go package (next to the
// regular .pb.go output) plus `schema/asyncapi.json` covering the whole
// request. Generated code calls runtime/events.Bus — pass any Bus
// implementation (NewInMemoryBus for tests, any Watermill-backed Bus for
// production transports).
package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/internal/eventsgen"
)

func main() {
	resp := eventsgen.Run(os.Stdin)
	out, err := proto.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "protoc-gen-events-go: marshal response: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "protoc-gen-events-go: write response: %v\n", err)
		os.Exit(1)
	}
}
