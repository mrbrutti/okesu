package db

import (
	"database/sql"
	"testing"
)

func TestScannerEndpointFallback(t *testing.T) {
	cases := []struct {
		name     string
		public   string
		internal sql.NullString
		want     string
	}{
		{"no override falls back", "public.example", sql.NullString{}, "public.example"},
		{"override wins", "public.example", sql.NullString{String: "internal.example", Valid: true}, "internal.example"},
		{"empty-but-valid override is ignored", "public.example", sql.NullString{String: "", Valid: true}, "public.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := TransportConfig{Endpoint: tc.public, EndpointInternal: tc.internal}
			if got := c.ScannerEndpoint(); got != tc.want {
				t.Errorf("ScannerEndpoint(): want %q got %q", tc.want, got)
			}
		})
	}
}
