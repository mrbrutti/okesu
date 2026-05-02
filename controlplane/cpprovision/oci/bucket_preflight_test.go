package oci

// Tests for the pre-flight helper functions extracted from BucketAccessKeys.
//
// checkFederatedUser and checkKeyLimit are pure functions that operate on OCI
// SDK value types — no HTTP transport needed, so these are full unit tests.
// End-to-end coverage of the BucketAccessKeys path (GetUser + ListCustomerSecretKeys
// round-trips) would require a live OCI endpoint or a custom http.RoundTripper
// shim; that is left as an integration concern.

import (
	"strings"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
)

// ── checkFederatedUser ────────────────────────────────────────────────────────

func TestCheckFederatedUser_NativeUser_NoError(t *testing.T) {
	// A native IAM user has no IdentityProviderId — should pass cleanly.
	name := "okesu-svc"
	user := identity.User{
		Name: &name,
		// IdentityProviderId deliberately nil
	}
	if err := checkFederatedUser(user); err != nil {
		t.Errorf("expected nil error for native user; got: %v", err)
	}
}

func TestCheckFederatedUser_EmptyProviderID_NoError(t *testing.T) {
	// An empty-string IdentityProviderId is treated the same as nil.
	name := "okesu-svc"
	empty := ""
	user := identity.User{
		Name:               &name,
		IdentityProviderId: &empty,
	}
	if err := checkFederatedUser(user); err != nil {
		t.Errorf("expected nil error for empty provider id; got: %v", err)
	}
}

func TestCheckFederatedUser_FederatedUser_ReturnsError(t *testing.T) {
	// A federated IDCS user must be rejected with a message naming the provider
	// and explaining which user type is required.
	name := "oracleidentitycloudservice/alice@example.com"
	idpID := "ocid1.saml2idp.oc1..aaaaabbbbcccc"
	user := identity.User{
		Name:               &name,
		IdentityProviderId: &idpID,
	}
	err := checkFederatedUser(user)
	if err == nil {
		t.Fatal("expected error for federated user; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "federated SSO user") {
		t.Errorf("error missing 'federated SSO user'; got: %s", msg)
	}
	if !strings.Contains(msg, idpID) {
		t.Errorf("error missing provider OCID %q; got: %s", idpID, msg)
	}
	if !strings.Contains(msg, name) {
		t.Errorf("error missing user name %q; got: %s", name, msg)
	}
}

// ── checkKeyLimit ─────────────────────────────────────────────────────────────

func TestCheckKeyLimit_NoKeys_NoError(t *testing.T) {
	if err := checkKeyLimit(nil); err != nil {
		t.Errorf("expected nil error for empty key list; got: %v", err)
	}
}

func TestCheckKeyLimit_OneActiveKey_NoError(t *testing.T) {
	id := "ocid1.credential.oc1..key1"
	ts := sdkTimeNow()
	items := []identity.CustomerSecretKeySummary{
		{
			Id:             &id,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateActive,
			TimeCreated:    &ts,
		},
	}
	if err := checkKeyLimit(items); err != nil {
		t.Errorf("expected nil for 1 active key; got: %v", err)
	}
}

func TestCheckKeyLimit_TwoActiveKeys_ReturnsError(t *testing.T) {
	id1, id2 := "ocid1.credential.oc1..key1", "ocid1.credential.oc1..key2"
	ts := sdkTimeNow()
	items := []identity.CustomerSecretKeySummary{
		{
			Id:             &id1,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateActive,
			TimeCreated:    &ts,
		},
		{
			Id:             &id2,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateActive,
			TimeCreated:    &ts,
		},
	}
	err := checkKeyLimit(items)
	if err == nil {
		t.Fatal("expected error for 2 active keys; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "per-user limit is 2") {
		t.Errorf("error missing limit message; got: %s", msg)
	}
	if !strings.Contains(msg, id1) {
		t.Errorf("error missing key id %q; got: %s", id1, msg)
	}
	if !strings.Contains(msg, id2) {
		t.Errorf("error missing key id %q; got: %s", id2, msg)
	}
}

func TestCheckKeyLimit_TwoActiveOneinactive_ReturnsError(t *testing.T) {
	// Inactive keys do not count toward the limit — but 2 active still triggers.
	id1, id2, id3 := "ocid1.credential.oc1..key1", "ocid1.credential.oc1..key2", "ocid1.credential.oc1..key3"
	ts := sdkTimeNow()
	items := []identity.CustomerSecretKeySummary{
		{
			Id:             &id1,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateActive,
			TimeCreated:    &ts,
		},
		{
			Id:             &id2,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateActive,
			TimeCreated:    &ts,
		},
		{
			Id:             &id3,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateInactive,
			TimeCreated:    &ts,
		},
	}
	if err := checkKeyLimit(items); err == nil {
		t.Error("expected error for 2 active + 1 inactive; got nil")
	}
}

func TestCheckKeyLimit_OnlyInactiveKeys_NoError(t *testing.T) {
	// All inactive — user can still create a new key.
	id := "ocid1.credential.oc1..oldkey"
	ts := sdkTimeNow()
	items := []identity.CustomerSecretKeySummary{
		{
			Id:             &id,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateInactive,
			TimeCreated:    &ts,
		},
		{
			Id:             &id,
			LifecycleState: identity.CustomerSecretKeySummaryLifecycleStateDeleted,
			TimeCreated:    &ts,
		},
	}
	if err := checkKeyLimit(items); err != nil {
		t.Errorf("expected nil for 0 active keys; got: %v", err)
	}
}

// sdkTimeNow is a test helper that wraps time.Now() in a common.SDKTime.
func sdkTimeNow() common.SDKTime {
	return common.SDKTime{Time: time.Now()}
}
