package oci

import (
	"strings"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
)

// TestChooseCompartment covers the three-way fallback:
//  1. override non-empty → returns override
//  2. override empty, creds.CompartmentID non-empty → returns CompartmentID
//  3. override empty, creds.CompartmentID empty     → returns TenancyOCID
func TestChooseCompartment(t *testing.T) {
	creds := &credentialPayload{
		TenancyOCID:   "ocid1.tenancy.oc1..tenancy",
		CompartmentID: "ocid1.compartment.oc1..comp",
	}

	t.Run("override wins", func(t *testing.T) {
		got := chooseCompartment("ocid1.compartment.oc1..override", creds)
		if got != "ocid1.compartment.oc1..override" {
			t.Errorf("got %q; want override OCID", got)
		}
	})

	t.Run("creds compartment when override empty", func(t *testing.T) {
		got := chooseCompartment("", creds)
		if got != creds.CompartmentID {
			t.Errorf("got %q; want %q", got, creds.CompartmentID)
		}
	})

	t.Run("tenancy root when both empty", func(t *testing.T) {
		c := &credentialPayload{TenancyOCID: "ocid1.tenancy.oc1..tenancy"}
		got := chooseCompartment("", c)
		if got != c.TenancyOCID {
			t.Errorf("got %q; want tenancy OCID", got)
		}
	})
}

// TestRegionByKey validates a sample of entries in the hand-built
// fallback map to catch typos without requiring a real OCI call.
func TestRegionByKey(t *testing.T) {
	cases := map[string]string{
		"PHX": "us-phoenix-1",
		"IAD": "us-ashburn-1",
		"FRA": "eu-frankfurt-1",
		"LHR": "uk-london-1",
		"NRT": "ap-tokyo-1",
	}
	for key, want := range cases {
		got, ok := regionByKey[key]
		if !ok {
			t.Errorf("regionByKey[%q] missing", key)
			continue
		}
		if got != want {
			t.Errorf("regionByKey[%q] = %q; want %q", key, got, want)
		}
	}
}

// TestHomeRegion_SDKRoundtrip verifies that common.StringToRegion
// correctly translates the lowercase 3-letter codes used by OCI's
// HomeRegionKey field. This exercises the SDK's own region table
// without making any network call.
func TestHomeRegion_SDKRoundtrip(t *testing.T) {
	// These keys appear as HomeRegionKey values in production tenancies.
	// We lowercase them here to match what homeRegion() passes to the SDK.
	sdkCases := []struct {
		key  string
		want string
	}{
		{"phx", "us-phoenix-1"},
		{"iad", "us-ashburn-1"},
		{"fra", "eu-frankfurt-1"},
		{"lhr", "uk-london-1"},
		{"nrt", "ap-tokyo-1"},
		{"syd", "ap-sydney-1"},
		{"icn", "ap-seoul-1"},
		{"yyz", "ca-toronto-1"},
	}
	for _, tc := range sdkCases {
		// homeRegion() calls common.StringToRegion(strings.ToLower(key))
		// and checks whether the returned value differs from the raw key.
		r := common.StringToRegion(tc.key)
		got := string(r)
		if got == "" || got == tc.key {
			t.Errorf("StringToRegion(%q) = %q — not translated (SDK table gap?)", tc.key, got)
			continue
		}
		if !strings.EqualFold(got, tc.want) {
			t.Errorf("StringToRegion(%q) = %q; want %q", tc.key, got, tc.want)
		}
	}
}
