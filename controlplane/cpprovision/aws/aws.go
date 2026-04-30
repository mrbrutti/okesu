// Package aws is the AWS implementation of cpprovision.Provisioner.
//
// Wraps aws-sdk-go-v2's EC2 RunInstances + DescribeInstances calls so a
// parent CP can spin up a child CP on EC2 directly from the Add-CP
// modal. The CloudParams free-form map carried in cp_provisions is
// decoded into a typed launchParams here — required fields are
// validated up-front so a typo in the modal surfaces as an actionable
// error before any cloud call is made.
//
// The provisioner does NOT attempt to be opinionated about networking.
// Operators bring their own VPC + subnet + security groups + AMI; we
// just RunInstances against them. The cloud-init the parent renders
// takes care of installing Docker + pulling the bundle.
//
// Errors from aws-sdk-go-v2 are surfaced verbatim into
// cp_provisions.error so an operator looking at the Federation page
// sees the real cloud message ("subnet 'subnet-abc' not found in
// vpc-xyz") rather than a wrapped one. The cost: the operator has to
// know a little EC2-speak, but that's correct — they're already
// provisioning into AWS and can read resource IDs.

package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// New returns a Provisioner ready to register against the parent
// CP's cpprovision.Registry. The provisioner is stateless — every
// Launch / Destroy call decodes its credential from the request
// payload, builds a fresh EC2 client, and tears it down.
func New() cpprovision.Provisioner {
	return &awsProvisioner{}
}

type awsProvisioner struct{}

func (awsProvisioner) Cloud() string { return "aws" }

// credentialPayload mirrors what the Settings → Cloud editor stores
// for AWS in cloud_credentials.encrypted_payload. Field names match
// the validators in api/cloud_credentials.go.
//
// Two auth modes are supported:
//
//   1. Static IAM user credentials — access_key_id + secret_access_key
//      (+ optional session_token for short-lived STS creds).
//   2. AssumeRole — role_arn alone, where the parent CP's own
//      EC2/instance/process credentials are used as the source identity.
//      external_id is forwarded to STS when provided.
//
// region is required either way: every EC2 call needs one and we
// don't want to silently default to us-east-1.
type credentialPayload struct {
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
	RoleARN         string `json:"role_arn,omitempty"`
	ExternalID      string `json:"external_id,omitempty"`
	Region          string `json:"region"`
}

// launchParams is what the +Add CP modal's cloud_params textarea
// must contain for AWS. Required fields are checked up-front so the
// 400 message lists exactly what's missing.
type launchParams struct {
	AMIID            string   `json:"ami_id"`
	InstanceType     string   `json:"instance_type"`
	SubnetID         string   `json:"subnet_id"`
	SecurityGroupIDs []string `json:"security_group_ids"`

	// Optional — leaving KeyName empty means the instance has no SSH
	// key pair attached; debugging requires Session Manager / EC2
	// Instance Connect.
	KeyName string `json:"key_name,omitempty"`

	// Optional IAM instance profile name (not ARN) for granting the
	// child CP its own AWS permissions.
	IAMInstanceProfile string `json:"iam_instance_profile,omitempty"`

	// AssignPublicIP defaults to true. When false, the instance gets
	// only a private IP — useful for VPCs with VPN / Direct Connect.
	// In v1 we don't validate the inverse case (no NAT to reach the
	// parent's bootstrap endpoint), so flip it off only when you're
	// sure the network can route.
	AssignPublicIP *bool `json:"assign_public_ip,omitempty"`

	// EBS root volume size in GiB. Defaults to whatever the AMI
	// declares; specify when you need extra headroom for the bundle
	// + Docker images.
	RootVolumeGB *int32 `json:"root_volume_gb,omitempty"`

	// Tags applied to the instance. We always add a Name tag below;
	// these merge on top.
	Tags map[string]string `json:"tags,omitempty"`
}

// Launch implements cpprovision.Provisioner.
func (p awsProvisioner) Launch(ctx context.Context, req cpprovision.LaunchRequest, log cpprovision.Logger) (*cpprovision.LaunchResult, error) {
	cred, err := decodeCredential(req.CredentialPayload)
	if err != nil {
		return nil, err
	}
	params, err := decodeLaunchParams(req.CloudParams)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = cred.Region
	}

	cfg, err := buildAWSConfig(ctx, cred, region)
	if err != nil {
		return nil, err
	}
	client := ec2.NewFromConfig(cfg)

	log.Logf("→ aws: RunInstances ami=%s type=%s subnet=%s", params.AMIID, params.InstanceType, params.SubnetID)

	encodedUserData := base64.StdEncoding.EncodeToString([]byte(req.CloudInitScript))

	displayName := fmt.Sprintf("okesu-cp-%s", strings.ToLower(req.DisplayName))

	tags := []ec2types.Tag{
		{Key: awssdk.String("Name"), Value: awssdk.String(displayName)},
		{Key: awssdk.String("okesu:role"), Value: awssdk.String("control-plane")},
	}
	for k, v := range params.Tags {
		tags = append(tags, ec2types.Tag{Key: awssdk.String(k), Value: awssdk.String(v)})
	}

	netIface := ec2types.InstanceNetworkInterfaceSpecification{
		DeviceIndex:              awssdk.Int32(0),
		SubnetId:                 awssdk.String(params.SubnetID),
		AssociatePublicIpAddress: awssdk.Bool(orDefault(params.AssignPublicIP, true)),
	}
	if len(params.SecurityGroupIDs) > 0 {
		netIface.Groups = params.SecurityGroupIDs
	}

	input := &ec2.RunInstancesInput{
		ImageId:           awssdk.String(params.AMIID),
		InstanceType:      ec2types.InstanceType(params.InstanceType),
		MinCount:          awssdk.Int32(1),
		MaxCount:          awssdk.Int32(1),
		UserData:          awssdk.String(encodedUserData),
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{netIface},
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		},
	}
	if strings.TrimSpace(params.KeyName) != "" {
		input.KeyName = awssdk.String(params.KeyName)
	}
	if strings.TrimSpace(params.IAMInstanceProfile) != "" {
		input.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{
			Name: awssdk.String(params.IAMInstanceProfile),
		}
	}
	if params.RootVolumeGB != nil && *params.RootVolumeGB > 0 {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{
			{
				DeviceName: awssdk.String("/dev/xvda"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize:          params.RootVolumeGB,
					DeleteOnTermination: awssdk.Bool(true),
					VolumeType:          ec2types.VolumeTypeGp3,
				},
			},
		}
	}

	resp, err := client.RunInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("aws RunInstances: %w", err)
	}
	if len(resp.Instances) == 0 || resp.Instances[0].InstanceId == nil {
		return nil, fmt.Errorf("aws RunInstances: empty response")
	}
	instanceID := *resp.Instances[0].InstanceId
	log.Logf("✓ aws: instance launched id=%s state=%s", instanceID, resp.Instances[0].State.Name)

	// Poll DescribeInstances until "running" (or terminal). RunInstances
	// returns "pending"; cloud-init only kicks in once we hit running.
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	publicIP := ""
	for {
		select {
		case <-pollCtx.Done():
			return nil, fmt.Errorf("aws: instance %s did not reach running within %s", instanceID, "10m")
		case <-time.After(15 * time.Second):
		}
		got, gerr := client.DescribeInstances(pollCtx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if gerr != nil {
			return nil, fmt.Errorf("aws DescribeInstances: %w", gerr)
		}
		state, inst := instanceState(got)
		log.Logf("  aws: state=%s", state)
		switch state {
		case ec2types.InstanceStateNameRunning:
			if inst != nil && inst.PublicIpAddress != nil && *inst.PublicIpAddress != "" {
				publicIP = *inst.PublicIpAddress
				log.Logf("  aws: public IP %s", publicIP)
			}
			result := &cpprovision.LaunchResult{
				ResourceID: instanceID,
				ConsoleURL: fmt.Sprintf("https://%s.console.aws.amazon.com/ec2/home?region=%s#InstanceDetails:instanceId=%s", region, region, instanceID),
				PublicIP:   publicIP,
			}
			return result, nil
		case ec2types.InstanceStateNameTerminated,
			ec2types.InstanceStateNameShuttingDown,
			ec2types.InstanceStateNameStopping,
			ec2types.InstanceStateNameStopped:
			return nil, fmt.Errorf("aws: instance reached terminal state %s before running", state)
		}
	}
}

// Destroy implements cpprovision.Provisioner.
func (p awsProvisioner) Destroy(ctx context.Context, resourceID, region string, credentialPayload []byte) error {
	cred, err := decodeCredential(credentialPayload)
	if err != nil {
		return err
	}
	if region == "" {
		region = cred.Region
	}
	cfg, err := buildAWSConfig(ctx, cred, region)
	if err != nil {
		return err
	}
	client := ec2.NewFromConfig(cfg)
	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{resourceID},
	})
	if err != nil {
		return fmt.Errorf("aws TerminateInstances: %w", err)
	}
	return nil
}

// ── helpers ────────────────────────────────────────────────────────

// buildAWSConfig assembles an aws.Config from the operator's stored
// credential. AssumeRole flow uses the parent CP process's default
// credential chain as the source identity, which means the parent
// host needs IAM permissions to call sts:AssumeRole on the target
// role — exactly mirroring how `aws sts assume-role` would behave at
// the CLI.
func buildAWSConfig(ctx context.Context, cred *credentialPayload, region string) (awssdk.Config, error) {
	if strings.TrimSpace(cred.RoleARN) != "" {
		base, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return awssdk.Config{}, fmt.Errorf("aws assume-role: load base config: %w", err)
		}
		stsClient := sts.NewFromConfig(base)
		roleProvider := stscreds.NewAssumeRoleProvider(stsClient, cred.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = "okesu-cp-provision"
			if strings.TrimSpace(cred.ExternalID) != "" {
				o.ExternalID = awssdk.String(cred.ExternalID)
			}
		})
		base.Credentials = awssdk.NewCredentialsCache(roleProvider)
		return base, nil
	}
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cred.AccessKeyID, cred.SecretAccessKey, cred.SessionToken,
		)),
	)
}

func decodeCredential(raw []byte) (*credentialPayload, error) {
	var c credentialPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("aws credential: parse: %w", err)
	}
	if strings.TrimSpace(c.Region) == "" {
		return nil, fmt.Errorf("aws credential missing fields: region")
	}
	hasStatic := strings.TrimSpace(c.AccessKeyID) != ""
	hasRole := strings.TrimSpace(c.RoleARN) != ""
	if !hasStatic && !hasRole {
		return nil, fmt.Errorf("aws credential: need either access_key_id+secret_access_key or role_arn")
	}
	if hasStatic && strings.TrimSpace(c.SecretAccessKey) == "" {
		return nil, fmt.Errorf("aws credential missing fields: secret_access_key")
	}
	return &c, nil
}

func decodeLaunchParams(in map[string]any) (*launchParams, error) {
	raw, _ := json.Marshal(in)
	var p launchParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("aws cloud_params: parse: %w", err)
	}
	missing := []string{}
	if strings.TrimSpace(p.AMIID) == "" {
		missing = append(missing, "ami_id")
	}
	if strings.TrimSpace(p.InstanceType) == "" {
		missing = append(missing, "instance_type")
	}
	if strings.TrimSpace(p.SubnetID) == "" {
		missing = append(missing, "subnet_id")
	}
	if len(p.SecurityGroupIDs) == 0 {
		missing = append(missing, "security_group_ids")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("aws cloud_params missing fields: %s", strings.Join(missing, ", "))
	}
	return &p, nil
}

// instanceState extracts the current state name and the first
// instance from a DescribeInstances response. Returns "" if the
// response is empty (which shouldn't happen for an instance we just
// launched, but be defensive).
func instanceState(out *ec2.DescribeInstancesOutput) (ec2types.InstanceStateName, *ec2types.Instance) {
	if out == nil {
		return "", nil
	}
	for _, r := range out.Reservations {
		if len(r.Instances) == 0 {
			continue
		}
		inst := &r.Instances[0]
		if inst.State == nil {
			return "", inst
		}
		return inst.State.Name, inst
	}
	return "", nil
}

func orDefault[T any](v *T, def T) T {
	if v == nil {
		return def
	}
	return *v
}
