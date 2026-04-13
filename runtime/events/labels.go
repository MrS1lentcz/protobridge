package events

import (
	"context"
	"strings"
)

// Label key prefix used in bus message headers. The prefix lets us tell
// label headers apart from arbitrary user-set headers (trace context,
// content-type, etc.) when the broadcast handler unpacks them into the
// JSON envelope and when the generated marshaler emits them to the wire.
const labelHeaderPrefix = "x-protobridge-label-"

// labelsCtxKey is the unexported context key under which WithLabels stashes
// the per-call label map. Generated Emit* helpers read it and merge the
// labels into the bus message headers; explicit msg headers passed to
// Publish always take precedence so callers can override per call.
type labelsCtxKey struct{}

// WithLabels returns a derived context carrying the given labels. Multiple
// calls compose: each WithLabels merges into (and overrides) labels already
// on the context. Generated Emit* helpers automatically forward whatever is
// on the context at publish time as message headers, so business logic
// only sets labels once near the call boundary (typically in a gRPC
// interceptor that derives them from the auth principal).
//
// kvs is a flat key/value list — odd-length input panics, mirroring the
// idiom of slog.With and httptest helpers.
func WithLabels(ctx context.Context, kvs ...string) context.Context {
	if len(kvs)%2 != 0 {
		panic("events.WithLabels: odd-length key/value list")
	}
	merged := map[string]string{}
	for k, v := range LabelsFromContext(ctx) {
		merged[k] = v
	}
	for i := 0; i < len(kvs); i += 2 {
		merged[kvs[i]] = kvs[i+1]
	}
	return context.WithValue(ctx, labelsCtxKey{}, merged)
}

// LabelsFromContext returns the labels previously stashed via WithLabels.
// Returns an empty map (never nil) when no labels are set, so callers can
// range over the result unconditionally.
func LabelsFromContext(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(labelsCtxKey{}).(map[string]string); ok && v != nil {
		return v
	}
	return map[string]string{}
}

// LabelsToHeaders prefixes every label key with labelHeaderPrefix and
// merges it into headers. The resulting map is what generated Emit*
// helpers pass to Bus.Publish — Watermill in turn flows those headers
// over the wire so the broadcast handler on the consuming side can
// extract them again via headersToLabels.
//
// Caller-supplied headers take precedence over context labels for any
// duplicate keys; this lets a single Emit* call override the ambient
// label scope when needed.
func LabelsToHeaders(labels, headers map[string]string) map[string]string {
	if len(labels) == 0 && len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels)+len(headers))
	for k, v := range labels {
		out[labelHeaderPrefix+k] = v
	}
	for k, v := range headers {
		out[k] = v // explicit headers win
	}
	return out
}

// HeadersToLabels strips the labelHeaderPrefix from every prefixed entry
// in headers and returns the resulting label map. Headers without the
// prefix are ignored. Used by the broadcast handler when assembling the
// JSON envelope.
func HeadersToLabels(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	var labels map[string]string
	for k, v := range headers {
		if !strings.HasPrefix(k, labelHeaderPrefix) {
			continue
		}
		if labels == nil {
			labels = map[string]string{}
		}
		labels[strings.TrimPrefix(k, labelHeaderPrefix)] = v
	}
	return labels
}

// LabelMatcher decides whether an event with eventLabels should be
// delivered to a subscriber whose principal carries principalLabels.
// The default matcher implements Kubernetes-style label-selector
// semantics: every event label must have a matching value on the
// principal. A nil matcher is treated as "match everything", preserving
// backwards compatibility with subscribers that don't care about labels.
type LabelMatcher func(principalLabels, eventLabels map[string]string) bool

// DefaultLabelMatcher returns true when every key in eventLabels is
// present on principalLabels with the same value. Empty eventLabels
// match any principal — events without labels are broadcast globally.
//
//	principal = {tenant_id: "abc", role: "admin"}
//	event     = {tenant_id: "abc"}                 → match
//	event     = {tenant_id: "xyz"}                 → no match
//	event     = {tenant_id: "abc", project_id: "p1"} → no match (project_id absent on principal)
//	event     = {}                                 → match
func DefaultLabelMatcher(principalLabels, eventLabels map[string]string) bool {
	for k, v := range eventLabels {
		if principalLabels[k] != v {
			return false
		}
	}
	return true
}
