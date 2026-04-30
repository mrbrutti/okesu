package cpprovision

import (
	"math"
	"testing"
)

func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestEstimateAWSKnown(t *testing.T) {
	est, ok := Estimate("aws", "t3.small", FlexInputs{})
	if !ok {
		t.Fatal("expected catalog hit for t3.small")
	}
	if !approxEq(est.USD, 0.0208) {
		t.Errorf("t3.small: want 0.0208, got %v", est.USD)
	}
	if est.Source != "fixed" {
		t.Errorf("t3.small: source=%q, want fixed", est.Source)
	}
}

func TestEstimateAWSUnknown(t *testing.T) {
	if _, ok := Estimate("aws", "x99.absurd", FlexInputs{}); ok {
		t.Error("expected catalog miss for x99.absurd")
	}
}

func TestEstimateOCIFlexNeedsInputs(t *testing.T) {
	if _, ok := Estimate("oci", "VM.Standard.E4.Flex", FlexInputs{}); ok {
		t.Error("flex shape with zero inputs should be unknown")
	}
}

func TestEstimateOCIFlexComputed(t *testing.T) {
	// E4.Flex: 0.025 per OCPU, 0.0015 per GB. 1 OCPU + 8 GB = 0.037.
	est, ok := Estimate("oci", "VM.Standard.E4.Flex", FlexInputs{OCPUs: 1, MemoryInGBs: 8})
	if !ok {
		t.Fatal("expected catalog hit")
	}
	if !approxEq(est.USD, 0.037) {
		t.Errorf("E4.Flex 1+8: want 0.037, got %v", est.USD)
	}
	if est.Source != "flex" {
		t.Errorf("source: want flex, got %q", est.Source)
	}
}

func TestMonthlyUSDConversion(t *testing.T) {
	// $0.10/hr × 730 = $73/mo
	if got := MonthlyUSD(0.10); !approxEq(got, 73.0) {
		t.Errorf("MonthlyUSD(0.10): want 73.0, got %v", got)
	}
}

func TestExtractInstanceShape(t *testing.T) {
	if got := ExtractInstanceShape("aws", map[string]any{"instance_type": "t3.small"}); got != "t3.small" {
		t.Errorf("aws extract: %q", got)
	}
	if got := ExtractInstanceShape("oci", map[string]any{"shape": "VM.Standard.E4.Flex"}); got != "VM.Standard.E4.Flex" {
		t.Errorf("oci extract: %q", got)
	}
	if got := ExtractInstanceShape("aws", map[string]any{"shape": "ignored"}); got != "" {
		t.Errorf("aws should not read shape: %q", got)
	}
	if got := ExtractInstanceShape("gcp", map[string]any{"instance_type": "x"}); got != "" {
		t.Errorf("unknown cloud should be empty: %q", got)
	}
}

func TestExtractFlexInputs(t *testing.T) {
	got := ExtractFlexInputs("oci", map[string]any{"ocpus": 2.0, "memory_in_gbs": 16.0})
	if !approxEq(got.OCPUs, 2.0) || !approxEq(got.MemoryInGBs, 16.0) {
		t.Errorf("flex extract: %+v", got)
	}
	// Non-OCI clouds always return zero
	if got := ExtractFlexInputs("aws", map[string]any{"ocpus": 4.0}); got.OCPUs != 0 {
		t.Errorf("aws flex extract should be zero: %+v", got)
	}
	// Mixed numeric types — int from JSON unmarshal can come as float64,
	// but JSON ints sometimes land as float64 or int depending on the
	// decoder; the extractor handles both.
	if got := ExtractFlexInputs("oci", map[string]any{"ocpus": 3, "memory_in_gbs": 24}); !approxEq(got.OCPUs, 3.0) {
		t.Errorf("int input: %+v", got)
	}
}
