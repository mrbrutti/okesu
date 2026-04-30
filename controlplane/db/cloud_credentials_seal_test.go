package db

import (
	"bytes"
	"testing"
)

// TestSealOpenCloudPayloadRoundtrip locks in the fix for the
// hkdf.New(nil, ...) panic that was crashing every
// /api/cloud-credentials POST. A passing round-trip implies
// deriveCloudKey returned a non-nil keystream; a regression to a
// nil hash constructor would panic here instead of returning a
// clean error.
func TestSealOpenCloudPayloadRoundtrip(t *testing.T) {
	// 32-byte master key — same shape as the base64-decoded
	// session_hmac_key cp_meta seeds at first boot.
	master := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte(`{"tenancy_ocid":"ocid1.tenancy.oc1..xxx","region":"us-ashburn-1"}`)

	ct, nonce, err := sealCloudPayload(master, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if len(ct) == 0 || len(nonce) == 0 {
		t.Fatalf("seal returned empty ct=%d nonce=%d", len(ct), len(nonce))
	}

	pt, err := openCloudPayload(master, ct, nonce)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("round-trip mismatch:\nwant %q\ngot  %q", plaintext, pt)
	}
}

// TestSealCloudPayloadDistinctNonces makes sure two consecutive
// seals of the same plaintext don't reuse a nonce — that would be
// catastrophic for AES-GCM (nonce reuse → key recovery).
func TestSealCloudPayloadDistinctNonces(t *testing.T) {
	master := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("hello")
	_, n1, err := sealCloudPayload(master, plaintext)
	if err != nil {
		t.Fatalf("seal #1: %v", err)
	}
	_, n2, err := sealCloudPayload(master, plaintext)
	if err != nil {
		t.Fatalf("seal #2: %v", err)
	}
	if bytes.Equal(n1, n2) {
		t.Fatalf("nonces must be distinct, got identical %x", n1)
	}
}

// TestDeriveCloudKeyShortMasterRejected covers the input-validation
// path so we don't regress to accepting a too-short master key
// (which would silently weaken the seal).
func TestDeriveCloudKeyShortMasterRejected(t *testing.T) {
	if _, err := deriveCloudKey(bytes.Repeat([]byte{0x01}, 8)); err == nil {
		t.Fatal("expected error for 8-byte master")
	}
}
