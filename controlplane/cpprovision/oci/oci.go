// Package oci is the OCI implementation of cpprovision.Provisioner.
//
// Wraps oci-go-sdk's compute LaunchInstance + GetInstance calls so a
// parent CP can spin up a child CP on OCI directly from the Add-CP
// modal. The CloudParams free-form map carried in cp_provisions is
// decoded into a typed launchParams here — required fields are
// validated up-front so a typo in the modal surfaces as an
// actionable error before any cloud call is made.
//
// The provisioner does NOT attempt to be opinionated about
// networking. Operators bring their own VCN + subnet + image OCID;
// we just LaunchInstance against them. The cloud-init the parent
// renders takes care of installing Docker + pulling the bundle.
//
// Errors from oci-go-sdk are surfaced verbatim into
// cp_provisions.error so an operator looking at the Federation page
// sees the real cloud message ("Subnet not found in compartment X")
// rather than a wrapped one. The cost: the operator has to know a
// little OCI-speak, but that's correct — they're already provisioning
// into OCI and can read OCIDs.

package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"

	"github.com/section9labs/okesu/controlplane/cpprovision"
)

// New returns a Provisioner ready to register against the parent
// CP's cpprovision.Registry. The provisioner is stateless — every
// Launch / Destroy call decodes its credential from the request
// payload, builds a fresh OCI client, and tears it down.
func New() cpprovision.Provisioner {
	return &ociProvisioner{}
}

type ociProvisioner struct{}

func (ociProvisioner) Cloud() string { return "oci" }

// credentialPayload mirrors what the Settings → Cloud editor stores
// for OCI in cloud_credentials.encrypted_payload. Field names match
// the validators in api/cloud_credentials.go.
type credentialPayload struct {
	TenancyOCID string `json:"tenancy_ocid"`
	UserOCID    string `json:"user_ocid"`
	Fingerprint string `json:"fingerprint"`
	PrivateKey  string `json:"private_key"`
	Region      string `json:"region"`
}

// launchParams is what the +Add CP modal's cloud_params textarea
// must contain for OCI. Required fields are checked up-front so the
// 400 message lists exactly what's missing.
type launchParams struct {
	CompartmentID      string `json:"compartment_id"`
	AvailabilityDomain string `json:"availability_domain"`
	SubnetID           string `json:"subnet_id"`
	ImageID            string `json:"image_id"`
	Shape              string `json:"shape"`

	// Flex-shape sizing. Required when shape is one of the *.Flex
	// kinds (most modern OCI shapes). Ignored for fixed shapes.
	OCPUs       *float32 `json:"ocpus,omitempty"`
	MemoryInGBs *float32 `json:"memory_in_gbs,omitempty"`

	// SSHAuthorizedKeys is appended to the instance's authorized_keys
	// so an operator can SSH in for debugging. Optional — leaving it
	// empty means the instance has no SSH access.
	SSHAuthorizedKeys string `json:"ssh_authorized_keys,omitempty"`

	// AssignPublicIP defaults to true. When false, the instance
	// gets only a private IP — useful for VCNs with VPN / FastConnect.
	// In v1 we don't validate the inverse case (no NAT to reach the
	// parent's bootstrap endpoint), so flip it off only when you're
	// sure the network can route.
	AssignPublicIP *bool `json:"assign_public_ip,omitempty"`
}

// Launch implements cpprovision.Provisioner.
func (p ociProvisioner) Launch(ctx context.Context, req cpprovision.LaunchRequest, log cpprovision.Logger) (*cpprovision.LaunchResult, error) {
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

	provider := common.NewRawConfigurationProvider(
		cred.TenancyOCID, cred.UserOCID, region, cred.Fingerprint,
		cred.PrivateKey, nil,
	)
	if _, err := provider.PrivateRSAKey(); err != nil {
		return nil, fmt.Errorf("oci credential: invalid private key: %w", err)
	}

	compute, err := core.NewComputeClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("oci compute client: %w", err)
	}

	log.Logf("→ oci: LaunchInstance compartment=%s ad=%s shape=%s",
		shortOCID(params.CompartmentID), params.AvailabilityDomain, params.Shape)

	encodedUserData := base64.StdEncoding.EncodeToString([]byte(req.CloudInitScript))
	metadata := map[string]string{
		"user_data": encodedUserData,
	}
	if strings.TrimSpace(params.SSHAuthorizedKeys) != "" {
		metadata["ssh_authorized_keys"] = params.SSHAuthorizedKeys
	}

	displayName := fmt.Sprintf("okesu-cp-%s", strings.ToLower(req.DisplayName))

	createVnic := core.CreateVnicDetails{
		SubnetId:        common.String(params.SubnetID),
		AssignPublicIp:  common.Bool(orDefault(params.AssignPublicIP, true)),
	}
	details := core.LaunchInstanceDetails{
		AvailabilityDomain: common.String(params.AvailabilityDomain),
		CompartmentId:      common.String(params.CompartmentID),
		Shape:              common.String(params.Shape),
		DisplayName:        common.String(displayName),
		Metadata:           metadata,
		CreateVnicDetails:  &createVnic,
		SourceDetails: core.InstanceSourceViaImageDetails{
			ImageId: common.String(params.ImageID),
		},
	}
	if isFlexShape(params.Shape) {
		if params.OCPUs == nil || params.MemoryInGBs == nil {
			return nil, fmt.Errorf("oci: shape %q is flex — require ocpus + memory_in_gbs in cloud_params", params.Shape)
		}
		details.ShapeConfig = &core.LaunchInstanceShapeConfigDetails{
			Ocpus:       params.OCPUs,
			MemoryInGBs: params.MemoryInGBs,
		}
	}

	resp, err := compute.LaunchInstance(ctx, core.LaunchInstanceRequest{
		LaunchInstanceDetails: details,
	})
	if err != nil {
		return nil, fmt.Errorf("oci LaunchInstance: %w", err)
	}
	instance := resp.Instance
	instanceID := common.String("")
	if instance.Id != nil {
		instanceID = instance.Id
	}
	log.Logf("✓ oci: instance launched id=%s state=%s", *instanceID, instance.LifecycleState)

	// Poll until RUNNING (or terminal). LaunchInstance returns
	// PROVISIONING; the cloud-init only kicks in once we hit RUNNING.
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	publicIP := ""
	for {
		select {
		case <-pollCtx.Done():
			return nil, fmt.Errorf("oci: instance %s did not reach RUNNING within %s", *instanceID, "10m")
		case <-time.After(15 * time.Second):
		}
		got, gerr := compute.GetInstance(pollCtx, core.GetInstanceRequest{InstanceId: instanceID})
		if gerr != nil {
			return nil, fmt.Errorf("oci GetInstance: %w", gerr)
		}
		log.Logf("  oci: state=%s", got.Instance.LifecycleState)
		switch got.Instance.LifecycleState {
		case core.InstanceLifecycleStateRunning:
			// Try to grab a public IP once running. Best-effort: a
			// ListVnicAttachments + GetVnic round-trip.
			pip, perr := lookupPublicIP(pollCtx, compute, provider, params.CompartmentID, *instanceID)
			if perr != nil {
				log.Logf("  oci: could not look up public IP: %v (continuing)", perr)
			} else if pip != "" {
				publicIP = pip
				log.Logf("  oci: public IP %s", publicIP)
			}
			result := &cpprovision.LaunchResult{
				ResourceID: *instanceID,
				ConsoleURL: fmt.Sprintf("https://cloud.oracle.com/compute/instances/%s?region=%s", *instanceID, region),
				PublicIP:   publicIP,
			}
			return result, nil
		case core.InstanceLifecycleStateTerminated,
			core.InstanceLifecycleStateTerminating,
			core.InstanceLifecycleStateStopped,
			core.InstanceLifecycleStateStopping:
			return nil, fmt.Errorf("oci: instance reached terminal state %s before RUNNING", got.Instance.LifecycleState)
		}
	}
}

// Destroy implements cpprovision.Provisioner.
func (p ociProvisioner) Destroy(ctx context.Context, resourceID, region string, credentialPayload []byte) error {
	cred, err := decodeCredential(credentialPayload)
	if err != nil {
		return err
	}
	if region == "" {
		region = cred.Region
	}
	provider := common.NewRawConfigurationProvider(
		cred.TenancyOCID, cred.UserOCID, region, cred.Fingerprint,
		cred.PrivateKey, nil,
	)
	compute, err := core.NewComputeClientWithConfigurationProvider(provider)
	if err != nil {
		return fmt.Errorf("oci compute client: %w", err)
	}
	_, err = compute.TerminateInstance(ctx, core.TerminateInstanceRequest{
		InstanceId:                common.String(resourceID),
		PreserveBootVolume:        common.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("oci TerminateInstance: %w", err)
	}
	return nil
}

// ── helpers ────────────────────────────────────────────────────────

func decodeCredential(raw []byte) (*credentialPayload, error) {
	var c credentialPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("oci credential: parse: %w", err)
	}
	missing := []string{}
	if c.TenancyOCID == "" {
		missing = append(missing, "tenancy_ocid")
	}
	if c.UserOCID == "" {
		missing = append(missing, "user_ocid")
	}
	if c.Fingerprint == "" {
		missing = append(missing, "fingerprint")
	}
	if c.PrivateKey == "" {
		missing = append(missing, "private_key")
	}
	if c.Region == "" {
		missing = append(missing, "region")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("oci credential missing fields: %s", strings.Join(missing, ", "))
	}
	return &c, nil
}

func decodeLaunchParams(in map[string]any) (*launchParams, error) {
	raw, _ := json.Marshal(in)
	var p launchParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("oci cloud_params: parse: %w", err)
	}
	missing := []string{}
	if p.CompartmentID == "" {
		missing = append(missing, "compartment_id")
	}
	if p.AvailabilityDomain == "" {
		missing = append(missing, "availability_domain")
	}
	if p.SubnetID == "" {
		missing = append(missing, "subnet_id")
	}
	if p.ImageID == "" {
		missing = append(missing, "image_id")
	}
	if p.Shape == "" {
		missing = append(missing, "shape")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("oci cloud_params missing fields: %s", strings.Join(missing, ", "))
	}
	return &p, nil
}

func isFlexShape(s string) bool {
	return strings.HasSuffix(s, ".Flex") || strings.HasSuffix(s, ".Flex.A1")
}

func orDefault[T any](v *T, def T) T {
	if v == nil {
		return def
	}
	return *v
}

// shortOCID truncates a long OCID to its tail for log readability.
// "ocid1.compartment.oc1..aaaaaaaa…xyz" → "…xyz".
func shortOCID(ocid string) string {
	if len(ocid) <= 12 {
		return ocid
	}
	return "…" + ocid[len(ocid)-10:]
}

// lookupPublicIP walks the VNIC attachments for a freshly-launched
// instance and returns the public IP of the primary VNIC. Best-effort:
// instances with assign_public_ip=false return "" with no error.
func lookupPublicIP(ctx context.Context, compute core.ComputeClient, provider common.ConfigurationProvider, compartmentID, instanceID string) (string, error) {
	attachments, err := compute.ListVnicAttachments(ctx, core.ListVnicAttachmentsRequest{
		CompartmentId: common.String(compartmentID),
		InstanceId:    common.String(instanceID),
	})
	if err != nil {
		return "", err
	}
	if len(attachments.Items) == 0 {
		return "", nil
	}
	vnetClient, err := core.NewVirtualNetworkClientWithConfigurationProvider(provider)
	if err != nil {
		return "", err
	}
	for _, a := range attachments.Items {
		if a.VnicId == nil {
			continue
		}
		vnic, err := vnetClient.GetVnic(ctx, core.GetVnicRequest{VnicId: a.VnicId})
		if err != nil {
			continue
		}
		if vnic.Vnic.PublicIp != nil && *vnic.Vnic.PublicIp != "" {
			return *vnic.Vnic.PublicIp, nil
		}
	}
	return "", nil
}
