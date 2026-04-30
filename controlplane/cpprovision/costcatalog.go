// Package-internal cost catalog for managed CP deploys (Phase 21.5).
//
// The catalog is a hand-curated lookup of the most common
// (cloud, shape/instance_type) → on-demand USD/hour. We use it to:
//
//   1. Surface an estimated $/mo in the +Add CP modal before submit.
//   2. Sum active provisions per credential for the budget check.
//
// Caveats — read these before believing a number:
//
//   - These are list prices, not the operator's negotiated rate.
//     Reserved instances, Savings Plans, OCI Universal Credits, etc.
//     can knock 30-70% off. Treat the estimate as an upper bound.
//   - Region pricing varies. We index US-East prices and accept the
//     blur — most operators provisioning a control plane don't pick
//     exotic regions for it.
//   - Flex shapes (OCI E4.Flex, A1.Flex, ...) price by OCPU + memory
//     separately. The catalog returns a per-OCPU + per-GB rate; the
//     caller multiplies by the cloud_params values it has on hand.
//   - Anything not in the catalog returns ok=false. The handler treats
//     unknowns as "$0 contribution to the budget" and lets the
//     operator submit, but the UI flags them so they can't sneak past
//     a budget by picking an off-catalog shape on purpose.
//
// Maintenance: when AWS / OCI publish material price changes, edit
// the tables below + bump the catalog version constant. We do NOT
// fetch live pricing from the cloud APIs at request time — that's
// per-call latency for operators on the +Add CP modal that nobody
// wants. Update this file like a changelog.

package cpprovision

import (
	"strings"
)

// CatalogVersion is appended to UI strings so operators know which
// catalog generation produced an estimate. Bump when prices change.
const CatalogVersion = "2026-04-29"

// HourlyEstimate is the cost the catalog believes one instance of a
// given shape costs to run for one hour. Returned by Estimate; zero
// values when ok=false.
type HourlyEstimate struct {
	USD       float64
	Source    string // "fixed" or "flex" — the latter means the rate already accounts for ocpus + memory
	Note      string // optional human note ("us-east-1 list price", etc.)
}

// Estimate returns a best-effort hourly USD rate for (cloud, shape).
// `flex` is the per-OCPU/memory inputs for OCI flex shapes; ignored
// for fixed shapes and for AWS (which prices instance types whole).
//
// ok=false means the catalog has no entry. Caller decides whether
// to block, warn, or treat as $0.
func Estimate(cloud, shape string, flex FlexInputs) (HourlyEstimate, bool) {
	cloud = strings.ToLower(strings.TrimSpace(cloud))
	shape = strings.TrimSpace(shape)
	switch cloud {
	case "oci":
		return estimateOCI(shape, flex)
	case "aws":
		return estimateAWS(shape)
	}
	return HourlyEstimate{}, false
}

// FlexInputs carries the per-OCPU / per-GB sizing the catalog needs
// to price OCI flex shapes. Ignored on AWS.
type FlexInputs struct {
	OCPUs       float64
	MemoryInGBs float64
}

// ── OCI ────────────────────────────────────────────────────────────
//
// OCI flex shapes price OCPU and memory separately. Numbers below
// are the published US-Ashburn / pay-as-you-go rates. Standard fixed
// shapes (VM.Standard1.x, VM.Standard2.x) are intentionally absent —
// they're decade-old SKUs the modal shouldn't be steering operators
// toward, and the catalog flagging them as "unknown" nudges them to
// pick a flex.

type ociFlexRate struct {
	PerOCPU      float64
	PerMemoryGB  float64
	Note         string
}

var ociFlexRates = map[string]ociFlexRate{
	// Intel
	"VM.Standard3.Flex": {PerOCPU: 0.0400, PerMemoryGB: 0.00250, Note: "Intel Ice Lake; us-ashburn-1 list"},
	// AMD
	"VM.Standard.E4.Flex": {PerOCPU: 0.0250, PerMemoryGB: 0.00150, Note: "AMD Rome; us-ashburn-1 list"},
	"VM.Standard.E5.Flex": {PerOCPU: 0.0270, PerMemoryGB: 0.00161, Note: "AMD Genoa; us-ashburn-1 list"},
	// Ampere ARM
	"VM.Standard.A1.Flex": {PerOCPU: 0.0100, PerMemoryGB: 0.00150, Note: "Ampere Altra; us-ashburn-1 list (free-tier covers 4 OCPU + 24 GB)"},
}

func estimateOCI(shape string, flex FlexInputs) (HourlyEstimate, bool) {
	if rate, ok := ociFlexRates[shape]; ok {
		// Flex shapes need both inputs to price; without them the
		// answer is meaningless. Surface as unknown so the caller
		// doesn't accidentally show $0/hr.
		if flex.OCPUs <= 0 || flex.MemoryInGBs <= 0 {
			return HourlyEstimate{}, false
		}
		usd := rate.PerOCPU*flex.OCPUs + rate.PerMemoryGB*flex.MemoryInGBs
		return HourlyEstimate{USD: usd, Source: "flex", Note: rate.Note}, true
	}
	return HourlyEstimate{}, false
}

// ── AWS ────────────────────────────────────────────────────────────
//
// AWS prices instance types whole. us-east-1 on-demand list prices
// from the AWS pricing page. Rounded to four decimals for display
// stability.

var awsHourlyRates = map[string]float64{
	// t3 burstable (Intel Skylake)
	"t3.nano":   0.0052,
	"t3.micro":  0.0104,
	"t3.small":  0.0208,
	"t3.medium": 0.0416,
	"t3.large":  0.0832,
	"t3.xlarge": 0.1664,
	"t3.2xlarge": 0.3328,
	// t4g burstable (Graviton2 ARM)
	"t4g.nano":   0.0042,
	"t4g.micro":  0.0084,
	"t4g.small":  0.0168,
	"t4g.medium": 0.0336,
	"t4g.large":  0.0672,
	"t4g.xlarge": 0.1344,
	"t4g.2xlarge": 0.2688,
	// m5 general-purpose (Intel)
	"m5.large":    0.096,
	"m5.xlarge":   0.192,
	"m5.2xlarge":  0.384,
	"m5.4xlarge":  0.768,
	// m6i general-purpose (Intel Ice Lake)
	"m6i.large":   0.096,
	"m6i.xlarge":  0.192,
	"m6i.2xlarge": 0.384,
	// m6g general-purpose (Graviton2)
	"m6g.large":   0.077,
	"m6g.xlarge":  0.154,
	"m6g.2xlarge": 0.308,
	// c5 compute-optimized (Intel)
	"c5.large":    0.085,
	"c5.xlarge":   0.170,
	"c5.2xlarge":  0.340,
	// c6i compute-optimized (Intel Ice Lake)
	"c6i.large":   0.085,
	"c6i.xlarge":  0.170,
	// c7g compute-optimized (Graviton3)
	"c7g.large":   0.0725,
	"c7g.xlarge":  0.1450,
}

func estimateAWS(instanceType string) (HourlyEstimate, bool) {
	if rate, ok := awsHourlyRates[instanceType]; ok {
		return HourlyEstimate{USD: rate, Source: "fixed", Note: "us-east-1 on-demand list"}, true
	}
	return HourlyEstimate{}, false
}

// MonthlyHours is the conventional 730 (365 days × 24 hours / 12
// months) cloud providers use for monthly pricing. Centralised so
// the catalog and the budget check agree on the conversion.
const MonthlyHours = 730.0

// MonthlyUSD converts a per-hour rate to a per-month rate using the
// industry-standard 730 hours/month convention.
func MonthlyUSD(hourlyUSD float64) float64 {
	return hourlyUSD * MonthlyHours
}

// ExtractInstanceShape pulls the shape / instance_type string out of
// a cloud_params map. Centralised here so the api layer + the worker
// both agree on which key to read.
func ExtractInstanceShape(cloud string, params map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "oci":
		if v, ok := params["shape"].(string); ok {
			return v
		}
	case "aws":
		if v, ok := params["instance_type"].(string); ok {
			return v
		}
	}
	return ""
}

// ExtractFlexInputs pulls (ocpus, memory_in_gbs) out of OCI cloud_params.
// Returns the zero value for non-OCI clouds — AWS doesn't need them.
func ExtractFlexInputs(cloud string, params map[string]any) FlexInputs {
	if strings.ToLower(strings.TrimSpace(cloud)) != "oci" {
		return FlexInputs{}
	}
	return FlexInputs{
		OCPUs:       toFloat(params["ocpus"]),
		MemoryInGBs: toFloat(params["memory_in_gbs"]),
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}
