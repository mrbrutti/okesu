// AWS S3 bucket provisioner. Reuses the credential payload shape from
// aws.go (access_key_id + secret_access_key, optionally session_token
// or role_arn) and the same SDK config builder.

package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// BucketProvisioner is the AWS S3 implementation of
// cpprovision.BucketProvisioner.
type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "aws" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, region string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	cfg, err := buildAWSConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: list buckets: %w", err)
	}
	infos := make([]cpprovision.BucketInfo, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		name := safeStr(b.Name)
		// LocationConstraint per bucket — not all buckets are in `region`.
		bucketRegion := region
		if loc, lErr := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: b.Name}); lErr == nil {
			if string(loc.LocationConstraint) != "" {
				bucketRegion = string(loc.LocationConstraint)
			} else {
				// Empty string == us-east-1 in AWS's older convention.
				bucketRegion = "us-east-1"
			}
		}
		infos = append(infos, cpprovision.BucketInfo{
			Name:     name,
			Region:   bucketRegion,
			Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", bucketRegion),
		})
	}
	return infos, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, region string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = creds.Region
	}
	cfg, err := buildAWSConfig(ctx, creds, region)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)

	// Idempotency: HEAD first; create on 404.
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &name}); err == nil {
		return &cpprovision.BucketInfo{
			Name:     name,
			Region:   region,
			Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", region),
		}, nil
	}

	in := &s3.CreateBucketInput{Bucket: &name}
	// us-east-1 doesn't accept a LocationConstraint; everywhere else does.
	if region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, in); err != nil {
		return nil, fmt.Errorf("aws: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{
		Name:     name,
		Region:   region,
		Endpoint: fmt.Sprintf("https://s3.%s.amazonaws.com", region),
	}, nil
}

func (p *BucketProvisioner) BucketAccessKeys(_ context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return "", "", errors.New("aws credential has no static access keys (role-only credentials are not supported for bucket provisioning yet)")
	}
	return creds.AccessKeyID, creds.SecretAccessKey, nil
}

func safeStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
