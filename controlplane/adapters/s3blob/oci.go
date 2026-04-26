package s3blob

import (
	"errors"
	"fmt"
	"strings"
)

// OCIObjectStorageEndpoint composes the S3-compatibility endpoint URL
// for OCI Object Storage from the tenancy namespace + region.
//
// OCI exposes object storage at:
//
//	https://<namespace>.compat.objectstorage.<region>.oraclecloud.com
//
// This helper exists so operators don't have to hand-construct the URL
// (which has historically been a fertile source of "I get 403"
// misconfigurations).
//
// `namespace` is the tenancy's Object Storage namespace, available in
// the OCI Console under Profile → Tenancy. NOT the tenancy OCID, NOT
// the compartment OCID — different field.
//
// `region` is e.g. "us-ashburn-1", "us-phoenix-1", "uk-london-1".
func OCIObjectStorageEndpoint(namespace, region string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	region = strings.TrimSpace(region)
	if namespace == "" {
		return "", errors.New("oci object storage: namespace required (Profile → Tenancy in the console)")
	}
	if region == "" {
		return "", errors.New("oci object storage: region required (e.g. us-ashburn-1)")
	}
	// minio-go takes "host:port" without scheme; UseSSL=true on the
	// Config gives us https.
	return fmt.Sprintf("%s.compat.objectstorage.%s.oraclecloud.com", namespace, region), nil
}

// CredsForOCIUser returns access/secret keys that authenticate against
// OCI Object Storage's S3-compat endpoint. These are NOT the tenancy
// API key — they're "Customer Secret Keys" minted in the IAM console:
//
//	Profile → My Profile → Customer Secret Keys → Generate
//
// The access key is what OCI calls the "Access Key" (random), the
// secret is the value shown only at generation time.
//
// We don't construct these — operators paste them into the
// --blob-access-key / --blob-secret-key flags or the equivalent env
// vars. This stub just documents the shape.
type OCISecretKeyHint struct {
	AccessKey string
	SecretKey string
}
