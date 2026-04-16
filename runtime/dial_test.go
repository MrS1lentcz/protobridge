package runtime_test

import (
	"net"
	"testing"

	"github.com/mrs1lentcz/protobridge/runtime"
)

func TestPreferIPFamily_Passthrough(t *testing.T) {
	cases := map[string]string{
		"":      "host:8080",
		"auto":  "host:8080",
		"AUTO":  "host:8080",
		"bogus": "host:8080",
	}
	for value, addr := range cases {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", value)
			if got := runtime.PreferIPFamily(addr); got != addr {
				t.Fatalf("want passthrough %q, got %q", addr, got)
			}
		})
	}
}

func TestPreferIPFamily_IPLiteralPassthrough(t *testing.T) {
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv4")
	// IP literal should not trigger a DNS lookup — passthrough.
	const addr = "10.0.0.2:9090"
	if got := runtime.PreferIPFamily(addr); got != addr {
		t.Fatalf("want %q, got %q", addr, got)
	}
	// IPv6 literal written in bracket form.
	const v6 = "[fdc4::1]:9090"
	if got := runtime.PreferIPFamily(v6); got != v6 {
		t.Fatalf("ipv6 literal passthrough failed: got %q", got)
	}
}

func TestPreferIPFamily_MalformedAddr(t *testing.T) {
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv4")
	// No port — SplitHostPort fails, function returns original.
	const addr = "no-port-here"
	if got := runtime.PreferIPFamily(addr); got != addr {
		t.Fatalf("malformed addr should pass through, got %q", got)
	}
}

func TestPreferIPFamily_IPv4Localhost(t *testing.T) {
	// "localhost" resolves on every developer platform. Assert that the
	// rewrite produced *some* IPv4 literal rather than pinning a specific
	// address — some hosts answer 127.0.0.1 from /etc/hosts, others
	// route via an alternative A record.
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv4")
	got := runtime.PreferIPFamily("localhost:1234")
	if got == "localhost:1234" {
		t.Skip("resolver did not return an IPv4 record for localhost on this host")
	}
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("rewrite produced malformed addr %q: %v", got, err)
	}
	if port != "1234" {
		t.Fatalf("port must be preserved, got %q", port)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("want IPv4 literal, got %q", host)
	}
}

func TestPreferIPFamily_IPv6Localhost(t *testing.T) {
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv6")
	got := runtime.PreferIPFamily("localhost:5555")
	if got == "localhost:5555" {
		t.Skip("resolver did not return an IPv6 record for localhost on this host")
	}
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("rewrite produced malformed addr %q: %v", got, err)
	}
	if port != "5555" {
		t.Fatalf("port must be preserved, got %q", port)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		t.Fatalf("want IPv6 literal, got %q", host)
	}
}

func TestPreferIPFamily_NoMatchingFamily(t *testing.T) {
	// IPv4-only literal resolves cleanly but the ipv6 lookup won't find a
	// matching record → passthrough rather than synthesising garbage.
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv6")
	const addr = "127.0.0.1:9090"
	if got := runtime.PreferIPFamily(addr); got != addr {
		t.Fatalf("ip literal passthrough failed: got %q", got)
	}
}

func TestPreferIPFamily_LookupFailure(t *testing.T) {
	t.Setenv("PROTOBRIDGE_DIAL_IP_FAMILY", "ipv4")
	// TLD .invalid is RFC2606-reserved to never resolve — the lookup must
	// fail and the function must degrade to passthrough rather than
	// hanging or returning empty.
	const addr = "nx.invalid:1234"
	if got := runtime.PreferIPFamily(addr); got != addr {
		t.Fatalf("want passthrough on lookup failure, got %q", got)
	}
}
