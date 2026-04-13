package events

import (
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

// NewInMemoryBus returns a Bus backed entirely by Watermill's gochannel
// implementation. Useful for tests, examples, and `protobridge dev` modes
// — no network, no persistence, no external services.
//
// Broadcast and durable paths each get their own gochannel instance so a
// BOTH-kind publish doesn't fan out twice to the same subscriber set —
// mirroring real backends where the two transports are genuinely separate
// (e.g. NATS Core vs JetStream). Durable consumer groups still don't
// load-balance in gochannel; for production semantics construct a
// *WatermillBus directly with real publishers/subscribers:
//
//	bus := &events.WatermillBus{
//	    BroadcastPublisher:  natsCorePub,
//	    BroadcastSubscriber: natsCoreSub,
//	    DurablePublisher:    jsPub,   // NATS JetStream
//	    DurableSubscriber:   jsSub,
//	}
//	defer bus.Close()
func NewInMemoryBus() *WatermillBus {
	logger := watermill.NopLogger{}
	// Two independent gochannels — broadcast deliveries must not leak into
	// durable subscribers and vice versa.
	broadcastPubSub := gochannel.NewGoChannel(gochannel.Config{}, logger)
	durablePubSub := gochannel.NewGoChannel(gochannel.Config{}, logger)
	return &WatermillBus{
		BroadcastPublisher:  broadcastPubSub,
		BroadcastSubscriber: broadcastPubSub,
		DurablePublisher:    durablePubSub,
		DurableSubscriber:   durablePubSub,
	}
}
