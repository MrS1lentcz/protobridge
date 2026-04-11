package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/internal/generator"
)

func main() {
	resp := generator.Run(os.Stdin)

	out, err := proto.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "protoc-gen-protobridge: failed to marshal response: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
