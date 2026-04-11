package main

import (
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/mrs1lentcz/protobridge/internal/generator"
)

func main() {
	resp := generator.Run(os.Stdin)

	out, err := proto.Marshal(resp)
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(out)
}
