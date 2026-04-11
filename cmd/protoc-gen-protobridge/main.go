package main

import (
	"io"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/mrs1lentcz/protobridge/internal/generator"
	"github.com/mrs1lentcz/protobridge/internal/parser"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}

	var req pluginpb.CodeGeneratorRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		panic(err)
	}

	api, err := parser.Parse(&req)
	if err != nil {
		writeError(err)
		return
	}

	resp, err := generator.Generate(api)
	if err != nil {
		writeError(err)
		return
	}

	out, err := proto.Marshal(resp)
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(out)
}

func writeError(err error) {
	msg := err.Error()
	resp := &pluginpb.CodeGeneratorResponse{
		Error: &msg,
	}
	out, _ := proto.Marshal(resp)
	os.Stdout.Write(out)
}
