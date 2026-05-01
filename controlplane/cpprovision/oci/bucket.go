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
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "oci" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, region string) ([]cpprovision.BucketInfo, error) {
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
		CompartmentId: common.String(creds.TenancyOCID), // root compartment by default
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

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, region string) (*cpprovision.BucketInfo, error) {
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
			CompartmentId: common.String(creds.TenancyOCID),
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
	provider := common.NewRawConfigurationProvider(
		creds.TenancyOCID, creds.UserOCID, creds.Region, creds.Fingerprint, creds.PrivateKey, nil)

	idClient, err := identity.NewIdentityClientWithConfigurationProvider(provider)
	if err != nil {
		return "", "", fmt.Errorf("oci: build identity client: %w", err)
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
