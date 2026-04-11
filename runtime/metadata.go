package runtime

import (
	"context"
	"encoding/base64"

	"google.golang.org/grpc/metadata"
)

const userMetadataKey = "x-protobridge-user"

// SetUserMetadata adds the serialized user data to outgoing gRPC metadata.
func SetUserMetadata(ctx context.Context, userData []byte) context.Context {
	encoded := base64.StdEncoding.EncodeToString(userData)
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = md.Copy()
	} else {
		md = metadata.MD{}
	}
	md.Set(userMetadataKey, encoded)
	return metadata.NewOutgoingContext(ctx, md)
}
