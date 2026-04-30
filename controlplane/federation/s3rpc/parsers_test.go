package s3rpc

import "testing"

func TestParentIDFromReqKey(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		selfID string
		want   string
	}{
		{
			name:   "well-formed",
			key:    "cp/global/outbound/east/req/abc123.json",
			selfID: "east",
			want:   "global",
		},
		{
			name:   "leading slash gets trimmed",
			key:    "/cp/global/outbound/east/req/abc.json",
			selfID: "east",
			want:   "global",
		},
		{
			name:   "self mismatch is rejected",
			key:    "cp/global/outbound/west/req/abc.json",
			selfID: "east",
			want:   "",
		},
		{
			name:   "missing req segment is rejected",
			key:    "cp/global/outbound/east/abc.json",
			selfID: "east",
			want:   "",
		},
		{
			name:   "extra segments are rejected",
			key:    "cp/global/outbound/east/req/sub/abc.json",
			selfID: "east",
			want:   "",
		},
		{
			name:   "wrong prefix is rejected",
			key:    "ws/global/outbound/east/req/abc.json",
			selfID: "east",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parentIDFromReqKey(tc.key, tc.selfID)
			if got != tc.want {
				t.Fatalf("parentIDFromReqKey(%q, %q) = %q, want %q",
					tc.key, tc.selfID, got, tc.want)
			}
		})
	}
}

func TestChildIDFromOutboundPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{
			name:   "with trailing slash",
			prefix: "cp/east/outbound/global/",
			want:   "east",
		},
		{
			name:   "without trailing slash",
			prefix: "cp/east/outbound/global",
			want:   "east",
		},
		{
			name:   "with leading slash",
			prefix: "/cp/east/outbound/global/",
			want:   "east",
		},
		{
			name:   "missing segments returns empty",
			prefix: "cp/east/",
			want:   "",
		},
		{
			name:   "wrong root returns empty",
			prefix: "ws/east/outbound/global/",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := childIDFromOutboundPrefix(tc.prefix)
			if got != tc.want {
				t.Fatalf("childIDFromOutboundPrefix(%q) = %q, want %q",
					tc.prefix, got, tc.want)
			}
		})
	}
}
