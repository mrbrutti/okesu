package aws

import (
	"strings"
	"testing"
)

func TestDecodeCredentialStatic(t *testing.T) {
	raw := []byte(`{"access_key_id":"AKIA","secret_access_key":"shh","region":"us-east-1"}`)
	c, err := decodeCredential(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.AccessKeyID != "AKIA" || c.Region != "us-east-1" {
		t.Fatalf("unexpected payload: %+v", c)
	}
}

func TestDecodeCredentialAssumeRole(t *testing.T) {
	raw := []byte(`{"role_arn":"arn:aws:iam::123:role/x","external_id":"abc","region":"us-west-2"}`)
	c, err := decodeCredential(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.RoleARN != "arn:aws:iam::123:role/x" || c.ExternalID != "abc" {
		t.Fatalf("unexpected payload: %+v", c)
	}
}

func TestDecodeCredentialErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"missing region", `{"access_key_id":"AKIA","secret_access_key":"shh"}`, "region"},
		{"no auth mode", `{"region":"us-east-1"}`, "access_key_id"},
		{"static missing secret", `{"access_key_id":"AKIA","region":"us-east-1"}`, "secret_access_key"},
		{"bad json", `{`, "parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeCredential([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing %q", err, tc.want)
			}
		})
	}
}

func TestDecodeLaunchParams(t *testing.T) {
	in := map[string]any{
		"ami_id":             "ami-1",
		"instance_type":      "t3.small",
		"subnet_id":          "subnet-1",
		"security_group_ids": []any{"sg-1"},
	}
	p, err := decodeLaunchParams(in)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.AMIID != "ami-1" || p.InstanceType != "t3.small" {
		t.Fatalf("unexpected: %+v", p)
	}
	if len(p.SecurityGroupIDs) != 1 || p.SecurityGroupIDs[0] != "sg-1" {
		t.Fatalf("security_group_ids not parsed: %+v", p.SecurityGroupIDs)
	}
}

func TestDecodeLaunchParamsMissing(t *testing.T) {
	_, err := decodeLaunchParams(map[string]any{"ami_id": "ami-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"instance_type", "subnet_id", "security_group_ids"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
