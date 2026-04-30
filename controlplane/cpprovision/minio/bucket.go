// MinIO is fully S3-compatible — we reuse the AWS S3 SDK with a
// custom endpoint pulled from the operator's MinIO cloud_credential.

package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// credentialPayload is what the Add Provider modal stores for MinIO.
// region is optional (most MinIO deployments are regionless; the SDK
// requires a region string regardless, so we default to "us-east-1").
type credentialPayload struct {
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region,omitempty"`
}

func decodeCredential(raw []byte) (*credentialPayload, error) {
	var c credentialPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("minio credential: parse: %w", err)
	}
	if c.Endpoint == "" {
		return nil, errors.New("minio credential missing fields: endpoint")
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("minio credential missing fields: access_key_id, secret_access_key")
	}
	return &c, nil
}

// BucketProvisioner is the MinIO implementation of cpprovision.BucketProvisioner.
type BucketProvisioner struct{}

func NewBucketProvisioner() cpprovision.BucketProvisioner { return &BucketProvisioner{} }

func (p *BucketProvisioner) Cloud() string { return "minio" }

func (p *BucketProvisioner) ListBuckets(ctx context.Context, credsRaw []byte, _ string) ([]cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	client := s3Client(creds)
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("minio: list buckets: %w", err)
	}
	infos := make([]cpprovision.BucketInfo, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		name := ""
		if b.Name != nil {
			name = *b.Name
		}
		infos = append(infos, cpprovision.BucketInfo{
			Name:     name,
			Region:   creds.Region,
			Endpoint: creds.Endpoint,
		})
	}
	return infos, nil
}

func (p *BucketProvisioner) EnsureBucket(ctx context.Context, credsRaw []byte, name, _ string) (*cpprovision.BucketInfo, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return nil, err
	}
	client := s3Client(creds)

	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &name}); err == nil {
		return &cpprovision.BucketInfo{Name: name, Region: creds.Region, Endpoint: creds.Endpoint}, nil
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &name}); err != nil {
		return nil, fmt.Errorf("minio: create bucket %q: %w", name, err)
	}
	return &cpprovision.BucketInfo{Name: name, Region: creds.Region, Endpoint: creds.Endpoint}, nil
}

func (p *BucketProvisioner) BucketAccessKeys(_ context.Context, credsRaw []byte) (string, string, error) {
	creds, err := decodeCredential(credsRaw)
	if err != nil {
		return "", "", err
	}
	return creds.AccessKeyID, creds.SecretAccessKey, nil
}

func s3Client(c *credentialPayload) *s3.Client {
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	return s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: awssdk.String(c.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
	})
}
