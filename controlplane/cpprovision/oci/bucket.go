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

	// Idempotency: HEAD first; create on 404.
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

	if _, err := client.CreateBucket(ctx, objectstorage.CreateBucketRequest{
		NamespaceName: ns.Value,
		CreateBucketDetails: objectstorage.CreateBucketDetails{
			Name:          common.String(name),
			CompartmentId: common.String(creds.TenancyOCID),
		},
	}); err != nil {
		return nil, fmt.Errorf("oci: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{
		Name:     name,
		Region:   region,
		Endpoint: ociS3Endpoint(namespace, region),
	}, nil
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
