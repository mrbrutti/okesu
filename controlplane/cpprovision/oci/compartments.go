// OCI compartments listing for the AddBucketWizard compartment selector.
//
// ListCompartments walks the entire compartment subtree rooted at
// creds.TenancyOCID with CompartmentIdInSubtree=true, iterating
// OpcNextPage until exhausted or the 5 000-compartment safety cap is
// reached. Only ACTIVE compartments are returned.

package oci

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

const maxCompartments = 5_000

// ListCompartments implements cpprovision.CompartmentLister.
//
// region is used only to build the Identity client endpoint; Identity
// reads are globally routable in OCI so either creds.Region or the
// caller-supplied region both work. Falls back to creds.Region when
// region is empty.
func (p *BucketProvisioner) ListCompartments(ctx context.Context, credsRaw []byte, region string) ([]cpprovision.Compartment, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	r := strings.TrimSpace(region)
	if r == "" {
		r = creds.Region
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, r, creds.Fingerprint, creds.PrivateKey, nil,
	)
	client, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci: build identity client: %w", err)
	}

	var out []cpprovision.Compartment
	var page *string
	for {
		req := identity.ListCompartmentsRequest{
			CompartmentId:          common.String(creds.TenancyOCID),
			AccessLevel:            identity.ListCompartmentsAccessLevelAny,
			CompartmentIdInSubtree: common.Bool(true),
			LifecycleState:         identity.CompartmentLifecycleStateActive,
			Limit:                  common.Int(1000),
		}
		if page != nil {
			req.Page = page
		}
		resp, err := client.ListCompartments(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("oci: ListCompartments: %w", err)
		}
		for _, c := range resp.Items {
			if c.Id == nil || c.Name == nil {
				continue
			}
			comp := cpprovision.Compartment{
				OCID: *c.Id,
				Name: *c.Name,
			}
			if c.CompartmentId != nil {
				comp.ParentID = *c.CompartmentId
			}
			comp.LifecycleState = string(c.LifecycleState)
			out = append(out, comp)
			if len(out) >= maxCompartments {
				break
			}
		}
		if len(out) >= maxCompartments || resp.OpcNextPage == nil || *resp.OpcNextPage == "" {
			break
		}
		page = resp.OpcNextPage
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
