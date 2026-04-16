package runtime

import (
	"log"
	"net"
	"os"
	"strings"
)

// PreferIPFamily rewrites a "host:port" dial address to pin a single IP
// family when PROTOBRIDGE_DIAL_IP_FAMILY is set. grpc-go's default
// resolver round-robins between IPv4 and IPv6 records — fine on a plain
// network, lethal inside Docker Desktop bridge networking where the
// published IPv6 address (fdc4:f303:9324::*) is not routable and the
// first dial attempt fails with "network is unreachable".
//
// Values:
//   - unset, "" or "auto" → passthrough, no rewrite (default).
//   - "ipv4" → resolve host, substitute the first A-record literal.
//   - "ipv6" → same for AAAA.
//
// Anything unexpected (IP literal input, lookup failure, no matching
// record, malformed "host:port") degrades to passthrough: the addr the
// caller supplied is returned unchanged so a misconfigured env var can't
// knock over an otherwise-healthy dial. Substitutions are logged at
// default log level so deploys can see which record won.
//
// Resolution is one-shot — grpc-go's own DNS resolver re-resolves when
// connections churn, so a long-lived dial that cycles would pick up
// fresh records anyway. The IP pin only affects the initial dial string.
func PreferIPFamily(addr string) string {
	family := strings.ToLower(strings.TrimSpace(os.Getenv("PROTOBRIDGE_DIAL_IP_FAMILY")))
	if family == "" || family == "auto" {
		return addr
	}
	if family != "ipv4" && family != "ipv6" {
		log.Printf("PROTOBRIDGE_DIAL_IP_FAMILY: unknown value %q, falling back to auto", family)
		return addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil {
		return addr
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		log.Printf("PROTOBRIDGE_DIAL_IP_FAMILY: lookup %q failed: %v, using original addr", host, err)
		return addr
	}
	for _, ip := range ips {
		if family == "ipv4" && ip.To4() != nil {
			resolved := net.JoinHostPort(ip.String(), port)
			log.Printf("PROTOBRIDGE_DIAL_IP_FAMILY=ipv4: %s → %s", addr, resolved)
			return resolved
		}
		if family == "ipv6" && ip.To4() == nil && ip.To16() != nil {
			resolved := net.JoinHostPort(ip.String(), port)
			log.Printf("PROTOBRIDGE_DIAL_IP_FAMILY=ipv6: %s → %s", addr, resolved)
			return resolved
		}
	}
	log.Printf("PROTOBRIDGE_DIAL_IP_FAMILY=%s: no matching record for %q, using original addr", family, host)
	return addr
}
