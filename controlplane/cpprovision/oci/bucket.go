// OCI Object Storage bucket provisioner. Uses the existing OCI
// signing config (tenancy + user + key) to call ListBuckets and
// CreateBucket. For S3-compatible access keys, calls IAM's
// CreateCustomerSecretKey for the user. Customer Secret Keys are
// per-user — a single OCI credential maps to a single S3-compat
// keypair, which is fine for v1.

package oci

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "oci" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, region, compartmentID string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, region, creds.Fingerprint, creds.PrivateKey, nil)

	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci: build objectstorage client: %w", err)
	}

	ns, err := client.GetNamespace(ctx, objectstorage.GetNamespaceRequest{})
	if err != nil {
		return nil, fmt.Errorf("oci: get namespace: %w", err)
	}
	namespace := safeStrPtr(ns.Value)

	resp, err := client.ListBuckets(ctx, objectstorage.ListBucketsRequest{
		NamespaceName: ns.Value,
		CompartmentId: common.String(chooseCompartment(compartmentID, creds)),
	})
	if err != nil {
		return nil, fmt.Errorf("oci: list buckets: %w", err)
	}
	out := make([]cpprovision.BucketInfo, 0, len(resp.Items))
	for _, b := range resp.Items {
		out = append(out, cpprovision.BucketInfo{
			Name:     safeStrPtr(b.Name),
			Region:   region,
			Endpoint: ociS3Endpoint(namespace, region),
		})
	}
	return out, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, region, compartmentID string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, region, creds.Fingerprint, creds.PrivateKey, nil)

	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci: build objectstorage client: %w", err)
	}

	ns, err := client.GetNamespace(ctx, objectstorage.GetNamespaceRequest{})
	if err != nil {
		return nil, fmt.Errorf("oci: get namespace: %w", err)
	}
	namespace := safeStrPtr(ns.Value)

	// Idempotency: HEAD first; create on 404. Errors other than 404
	// (e.g. permission gaps where the user can CreateBucket but not
	// GetBucket) fall through to the create path; if that 409s with
	// BucketAlreadyExists, we re-probe and surface a clearer error
	// distinguishing "bucket exists, you can use it" from "bucket
	// exists in your tenancy but your IAM user can't see it" — OCI's
	// own 409 message conflates the two.
	if _, headErr := client.GetBucket(ctx, objectstorage.GetBucketRequest{
		NamespaceName: ns.Value,
		BucketName:    common.String(name),
	}); headErr == nil {
		return &cpprovision.BucketInfo{
			Name:     name,
			Region:   region,
			Endpoint: ociS3Endpoint(namespace, region),
		}, nil
	}

	_, createErr := client.CreateBucket(ctx, objectstorage.CreateBucketRequest{
		NamespaceName: ns.Value,
		CreateBucketDetails: objectstorage.CreateBucketDetails{
			Name:          common.String(name),
			CompartmentId: common.String(chooseCompartment(compartmentID, creds)),
		},
	})
	if createErr == nil {
		return &cpprovision.BucketInfo{
			Name:     name,
			Region:   region,
			Endpoint: ociS3Endpoint(namespace, region),
		}, nil
	}

	// 409 BucketAlreadyExists is OCI's deliberately ambiguous "either
	// the bucket exists or you're not authorized" response. Re-probe
	// with GetBucket: if that now succeeds, treat the create as
	// idempotently satisfied. If the probe still fails, the operator
	// has an IAM gap — give them the policy needed to fix it.
	if se, ok := common.IsServiceError(createErr); ok &&
		se.GetHTTPStatusCode() == 409 &&
		se.GetCode() == "BucketAlreadyExists" {
		if _, headErr := client.GetBucket(ctx, objectstorage.GetBucketRequest{
			NamespaceName: ns.Value,
			BucketName:    common.String(name),
		}); headErr == nil {
			return &cpprovision.BucketInfo{
				Name:     name,
				Region:   region,
				Endpoint: ociS3Endpoint(namespace, region),
			}, nil
		}
		return nil, fmt.Errorf(
			"oci: bucket %q already exists in tenancy namespace %q but your IAM user cannot read it. "+
				"Either grant `inspect buckets` (and ideally `read buckets`) on the compartment that owns the bucket, "+
				"or pick a different bucket name. OCI bucket names are unique per-tenancy regardless of compartment. "+
				"Original error: %w",
			name, namespace, createErr,
		)
	}

	// 401/403/404 from CreateBucket itself = IAM permission gap on
	// `manage buckets` in the target compartment.
	if se, ok := common.IsServiceError(createErr); ok {
		switch se.GetHTTPStatusCode() {
		case 401, 403, 404:
			return nil, fmt.Errorf(
				"oci: cannot create bucket %q — IAM user lacks `manage buckets` in the target compartment. "+
					"Required policy: `allow group <your-group> to manage buckets in compartment <name>`. "+
					"Original error: %w",
				name, createErr,
			)
		}
	}

	return nil, fmt.Errorf("oci: create bucket %q: %w", name, createErr)
}

func (p *BucketProvisioner) BucketAccessKeys(ctx context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}

	// Identity mutation calls (CreateCustomerSecretKey) must be made
	// against the user's home region — OCI rejects them with "Please
	// go to your home region" if issued against any other region.
	// Discover the home region first, then pin the Identity client to it.
	hr, err := homeRegion(ctx, creds)
	if err != nil {
		// Fall back to creds.Region with a warning rather than hard-
		// failing. Some tenancy setups (e.g. single-region) may work
		// even without this, and a degraded attempt beats a blank error.
		log.Printf("oci: warning: could not determine home region for tenancy %s: %v — falling back to creds.Region %s",
			creds.TenancyOCID, err, creds.Region)
		hr = creds.Region
	}

	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, hr, creds.Fingerprint, creds.PrivateKey, nil)

	idClient, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return "", "", fmt.Errorf("oci: build identity client: %w", err)
	}

	// Pre-flight #1: reject federated (SSO) users early with a clear message.
	// CreateCustomerSecretKey returns an opaque HTTP 401 IdcsConversionError for
	// federated users; catching it here gives operators an actionable explanation.
	userResp, err := idClient.GetUser(ctx, identity.GetUserRequest{
		UserId: common.String(creds.UserOCID),
	})
	if err != nil {
		return "", "", fmt.Errorf("oci: GetUser to verify provider type: %w", err)
	}
	if err := checkFederatedUser(userResp.User); err != nil {
		return "", "", err
	}

	// Pre-flight #2: surface the per-user 2-key limit clearly.
	// CreateCustomerSecretKey returns an opaque HTTP 400 LimitExceeded on the
	// third key; showing the existing keys lets operators decide which to delete.
	keysResp, err := idClient.ListCustomerSecretKeys(ctx, identity.ListCustomerSecretKeysRequest{
		UserId: common.String(creds.UserOCID),
	})
	if err != nil {
		return "", "", fmt.Errorf("oci: list existing Customer Secret Keys: %w", err)
	}
	if err := checkKeyLimit(keysResp.Items); err != nil {
		return "", "", err
	}

	resp, err := idClient.CreateCustomerSecretKey(ctx, identity.CreateCustomerSecretKeyRequest{
		UserId: common.String(creds.UserOCID),
		CreateCustomerSecretKeyDetails: identity.CreateCustomerSecretKeyDetails{
			DisplayName: common.String("okesu-bucket-provisioner"),
		},
	})
	if err != nil {
		// Surface the verbatim error so operators can act on permission
		// gaps (e.g., "Authorization failed... requires manage
		// customer-secret-keys").
		return "", "", fmt.Errorf("oci: create customer secret key: %w", err)
	}
	access := safeStrPtr(resp.Id)
	secret := safeStrPtr(resp.Key)
	if access == "" || secret == "" {
		return "", "", errors.New("oci: customer secret key creation returned empty access/secret")
	}
	return access, secret, nil
}

// chooseCompartment returns the effective compartment OCID to use for
// OCI object-storage operations. Priority:
//  1. override — if non-empty, use it directly (caller-supplied compartment).
//  2. creds.CompartmentID — the default compartment stored in the credential.
//  3. creds.TenancyOCID — root compartment fallback.
func chooseCompartment(override string, creds *credentialPayload) string {
	if override != "" {
		return override
	}
	if creds.CompartmentID != "" {
		return creds.CompartmentID
	}
	return creds.TenancyOCID
}

// homeRegion discovers the canonical region name for the tenancy's
// home region by calling Identity's GetTenancy and translating the
// returned HomeRegionKey (e.g. "PHX") to a full region name
// (e.g. "us-phoenix-1") using the OCI SDK's region table.
//
// The Identity client used here is built against creds.Region only
// for the purpose of the GetTenancy read — that call is globally
// routable and does not require the home region.
func homeRegion(ctx context.Context, creds *credentialPayload) (string, error) {
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, creds.Region, creds.Fingerprint, creds.PrivateKey, nil)

	idClient, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return "", fmt.Errorf("oci: build identity client for home-region discovery: %w", err)
	}

	resp, err := idClient.GetTenancy(ctx, identity.GetTenancyRequest{
		TenancyId: common.String(creds.TenancyOCID),
	})
	if err != nil {
		return "", fmt.Errorf("oci: GetTenancy: %w", err)
	}

	key := safeStrPtr(resp.Tenancy.HomeRegionKey)
	if key == "" {
		return "", errors.New("oci: GetTenancy returned empty HomeRegionKey")
	}

	// common.StringToRegion accepts both short keys ("phx") and full
	// names ("us-phoenix-1"). It lowercases the input before lookup.
	r := common.StringToRegion(strings.ToLower(key))
	name := string(r)
	if name == "" || name == strings.ToLower(key) {
		// SDK didn't recognise the key — fall back to the hand-built map.
		if mapped, ok := regionByKey[strings.ToUpper(key)]; ok {
			return mapped, nil
		}
		return "", fmt.Errorf("oci: unknown HomeRegionKey %q", key)
	}
	return name, nil
}

// regionByKey maps OCI's 3-letter home-region codes to canonical
// region names. Used as a fallback when common.StringToRegion returns
// the raw key unchanged (e.g. for newly-added regions not yet in the
// SDK's built-in table).
var regionByKey = map[string]string{
	"PHX": "us-phoenix-1",
	"IAD": "us-ashburn-1",
	"FRA": "eu-frankfurt-1",
	"LHR": "uk-london-1",
	"YYZ": "ca-toronto-1",
	"NRT": "ap-tokyo-1",
	"SYD": "ap-sydney-1",
	"ICN": "ap-seoul-1",
	"BOM": "ap-mumbai-1",
	"GRU": "sa-saopaulo-1",
	"ZRH": "eu-zurich-1",
	"AMS": "eu-amsterdam-1",
}

// ociS3Endpoint returns the S3-compatible URL for a namespace+region.
// Operators write to this URL with the Customer Secret Keys returned
// by BucketAccessKeys.
func ociS3Endpoint(namespace, region string) string {
	return fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com",
		strings.TrimSpace(namespace), strings.TrimSpace(region))
}

func safeStrPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// checkFederatedUser returns a clear, actionable error if user is a federated
// SSO user (IDCS / Identity Domain). Such users cannot own Customer Secret Keys
// and OCI surfaces a cryptic HTTP 401 IdcsConversionError on the create call.
// Exported for unit-testing without a live OCI endpoint.
func checkFederatedUser(user identity.User) error {
	if user.IdentityProviderId == nil || *user.IdentityProviderId == "" {
		return nil
	}
	return fmt.Errorf(
		"oci: cannot create Customer Secret Keys for user %q — this is a federated SSO user (provider %q). "+
			"Customer Secret Keys can only be created on native IAM users. Either configure okesu-cp's cloud "+
			"credential to use a native IAM user (one without identity_provider_id), or create a dedicated "+
			"service user in your Default identity domain with `manage object-family in tenancy` and use that.",
		safeStrPtr(user.Name), safeStrPtr(user.IdentityProviderId),
	)
}

// checkKeyLimit returns a clear, actionable error if the user already has 2
// active Customer Secret Keys (the per-user OCI limit). OCI surfaces a cryptic
// HTTP 400 LimitExceeded on the third create call; catching it here lets
// operators see which existing keys are blocking the operation.
// Exported for unit-testing without a live OCI endpoint.
func checkKeyLimit(items []identity.CustomerSecretKeySummary) error {
	activeCount := 0
	var existing []string
	for _, k := range items {
		if k.LifecycleState == identity.CustomerSecretKeySummaryLifecycleStateActive {
			activeCount++
			id := safeStrPtr(k.Id)
			ts := ""
			if k.TimeCreated != nil {
				ts = k.TimeCreated.Time.Format(time.RFC3339)
			}
			existing = append(existing, fmt.Sprintf("%s (created %s)", id, ts))
		}
	}
	if activeCount >= 2 {
		return fmt.Errorf(
			"oci: user already has %d active Customer Secret Keys (per-user limit is 2). "+
				"Existing: [%s]. Delete an unused key in OCI Console "+
				"(Identity → Users → API Keys → Customer Secret Keys), or use the existing key.",
			activeCount, strings.Join(existing, "; "),
		)
	}
	return nil
}
