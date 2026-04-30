package db

import "testing"

func TestCloudCredentialKinds_AcceptsMinio(t *testing.T) {
	found := false
	for _, k := range AllowedCloudKinds {
		if k == "minio" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'minio' in AllowedCloudKinds; got %v", AllowedCloudKinds)
	}
}
