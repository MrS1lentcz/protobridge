package events

import (
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

// NewInMemoryBus returns a Bus backed entirely by Watermill's gochannel
// implementation. Useful for tests, examples, and `protobridge dev` modes
// — no network, no persistence, no external services.
//
// Both broadcast and durable paths share one gochannel instance; gochannel
// fan-outs to every subscriber, so durable consumer groups don't load-balance
// here. For production semantics construct a *WatermillBus directly with
// real publishers/subscribers in its struct fields:
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
	pubsub := gochannel.NewGoChannel(gochannel.Config{
		// BlockPublishUntilSubscriberAck = false (default): publishes return
		// immediately even if no subscriber is connected — matches the
		// fire-and-forget broadcast contract.
	}, logger)
	return &WatermillBus{
		BroadcastPublisher:  pubsub,
		BroadcastSubscriber: pubsub,
		DurablePublisher:    pubsub,
		DurableSubscriber:   pubsub,
	}
}
