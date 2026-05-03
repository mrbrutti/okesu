package api

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestPeekMessageType(t *testing.T) {
	cases := []struct {
		in   []byte
		want byte
		ok   bool
	}{
		{[]byte{0, 0xff, 0xff}, 0, true}, // sync
		{[]byte{1, 0xa0, 0x01}, 1, true}, // awareness
		{[]byte{2}, 2, true},             // session-end
		{[]byte{}, 0, false},             // empty: not ok
	}
	for _, c := range cases {
		got, ok := peekMessageType(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("peekMessageType(%v) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestEncodeSyncStep1Empty(t *testing.T) {
	got := encodeSyncStep1Empty()
	// Wire format: type byte (0) + sub-type varint (0) + state-vector
	// length varint (0) — empty state vector means "give me everything".
	want := []byte{0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPeekSyncSubType(t *testing.T) {
	// type=0, sub-type=2 (sync-step-2 reply)
	got, ok := peekSyncSubType([]byte{0, 2, 0xff})
	if !ok || got != 2 {
		t.Errorf("got (%d, %v), want (2, true)", got, ok)
	}
	// type=1 (awareness, not sync) — should not return sub-type
	_, ok = peekSyncSubType([]byte{1, 0xa0})
	if ok {
		t.Errorf("type=1 should not yield a sub-type")
	}
}

func TestDecodeYTextBody_EmptyDoc(t *testing.T) {
	// An empty Yjs document encodeStateAsUpdate is two bytes: [0, 0].
	got, err := decodeYTextBody([]byte{0, 0}, "body")
	if err != nil {
		t.Fatalf("decodeYTextBody: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDecodeYTextBody_HelloWorld(t *testing.T) {
	// Fixture: Y.encodeStateAsUpdate(doc) where doc has a Y.Text named
	// "body" with the content "hello world".
	//
	// To regenerate: from web/, run
	//   node -e '
	//     const Y = require("yjs");
	//     const d = new Y.Doc();
	//     d.getText("body").insert(0, "hello world");
	//     console.log(Buffer.from(Y.encodeStateAsUpdate(d)).toString("hex"));
	//   '
	fixtureHex := "0101cce5ade20100040104626f64790b68656c6c6f20776f726c6400"

	if fixtureHex == "" {
		t.Skip("decodeYTextBody happy-path test requires a generated fixture; see test comment")
	}

	fixture := mustHexDecode(t, fixtureHex)
	got, err := decodeYTextBody(fixture, "body")
	if err != nil {
		t.Fatalf("decodeYTextBody: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestDecodeYTextBody_MultiLine(t *testing.T) {
	// Fixture: a Y.Text named "body" containing "hello\n  world".
	// Multi-line + indentation exercises that the decoder copies raw
	// UTF-8 bytes verbatim and doesn't strip whitespace.
	fixtureHex := "0101ede3f6820400040104626f64790d68656c6c6f0a2020776f726c6400"

	fixture := mustHexDecode(t, fixtureHex)
	got, err := decodeYTextBody(fixture, "body")
	if err != nil {
		t.Fatalf("decodeYTextBody: %v", err)
	}
	if got != "hello\n  world" {
		t.Errorf("got %q, want %q", got, "hello\n  world")
	}
}

func TestDecodeYTextBody_WrongFieldName(t *testing.T) {
	// Same hello-world fixture, but ask for a field that doesn't exist.
	// The decoder should return an empty string (not panic).
	fixture := mustHexDecode(t, "0101cce5ade20100040104626f64790b68656c6c6f20776f726c6400")
	got, err := decodeYTextBody(fixture, "title")
	if err != nil {
		t.Fatalf("decodeYTextBody: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty (field name does not match)", got)
	}
}

func TestDecodeYTextBody_MalformedInputs(t *testing.T) {
	// For nil and empty input, the contract is: non-nil error.
	for _, c := range [][]byte{nil, {}} {
		_, err := decodeYTextBody(c, "body")
		if err == nil {
			t.Errorf("decodeYTextBody(%v) returned nil error; want non-nil", c)
		}
	}
	// For other malformed inputs the contract is just no-panic; any
	// (string, error) pair is acceptable.
	for _, c := range [][]byte{
		{0x01},               // numClients=1, then EOF
		{0x01, 0x01},         // numClients=1, numStructs=1, then clientID EOF
		{0xff, 0xff, 0xff},   // garbage varints
		{0x01, 0x01, 0x00, 0x00, 0x04, 0x01, 0x04, 0x62}, // truncated mid-string
	} {
		_, _ = decodeYTextBody(c, "body")
	}
}

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	return b
}
