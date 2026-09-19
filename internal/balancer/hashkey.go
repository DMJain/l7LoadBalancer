package balancer

import (
	"net"
	"net/http"
)

// requestHashKey returns the consistent-hash key for r: the client address
// from r.RemoteAddr with the port stripped. Both consistent-hash selectors
// (naiveConsistentHash and ConsistentHashBoundedLoads) hash this string to
// find a ring position, so one client sticks to one backend across requests
// and across ephemeral source ports. See CONTEXT.md "Hash key".
//
// A RemoteAddr that SplitHostPort cannot parse — no port at all, or empty —
// is returned unchanged rather than treated as an error: the key only has to
// be stable per client, and a malformed peer address is still a stable one.
// All such requests therefore hash to the same ring position and route to one
// backend. That is intentional: a malformed RemoteAddr is a bug in the caller
// or an intermediate proxy, and concentrating those requests surfaces it in
// one backend's logs instead of smearing it across the fleet. Hashing, not
// this function, is what maps a key to a ring position.
//
// Known limitation: RemoteAddr is the immediate peer's address. Behind
// another proxy this is that proxy's IP, not the original client's;
// X-Forwarded-For resolution is deliberately out of scope for Sprint 2.
func requestHashKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
