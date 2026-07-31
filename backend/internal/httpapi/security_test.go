package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustsRealIPOnlyFromLoopback(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		realIP     string
		want       string
	}{
		{name: "loopback proxy", remoteAddr: "127.0.0.1:1234", realIP: "203.0.113.7", want: "203.0.113.7"},
		{name: "IPv6 loopback proxy", remoteAddr: "[::1]:1234", realIP: "2001:db8::7", want: "2001:db8::7"},
		{name: "untrusted remote", remoteAddr: "198.51.100.9:1234", realIP: "203.0.113.7", want: "198.51.100.9"},
		{name: "malformed header", remoteAddr: "127.0.0.1:1234", realIP: "203.0.113.7, 198.51.100.9", want: "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			r.Header.Set("X-Real-IP", tt.realIP)
			if got := clientIP(r); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
