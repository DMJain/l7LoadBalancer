package balancer

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRequestHashKey pins the hash-key derivation: the client IP from
// RemoteAddr, with the port stripped. Both consistent-hash selectors hash
// this string, so stripping the port — not hashing the raw RemoteAddr — is
// what makes two requests from the same client, arriving on different
// ephemeral ports, share a ring position. See CONTEXT.md "Hash key".
func TestRequestHashKey(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "ipv4 strips port", remoteAddr: "203.0.113.7:54321", want: "203.0.113.7"},
		{name: "ipv6 strips port and brackets", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "host already bare is unchanged", remoteAddr: "203.0.113.7", want: "203.0.113.7"},
		{name: "empty is unchanged", remoteAddr: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr

			assert.Equal(t, tt.want, requestHashKey(r))
		})
	}
}
