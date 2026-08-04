// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	networkingtypes "github.com/evroc-oss/evroc-go-sdk/types/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evroc-oss/cluster-api-provider-evroc/internal/cloud"
)

// credentialsPaths lists candidate locations for the credentials YAML file,
// relative to the working directory.  The integration tests can be run from
// both the repo root (`go test ./test/integration/...`) and from the
// test/integration directory itself, so we try multiple paths.
var credentialsPaths = []string{
	"test/e2e/credentials.yaml",
	"../../test/e2e/credentials.yaml",
}

// newCloudClient creates a cloud client by reading credentials.yaml directly.
// No environment variables are needed.
func newCloudClient(t *testing.T) cloud.ClientInterface {
	t.Helper()

	var data []byte
	var err error
	for _, p := range credentialsPaths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	require.NoError(t, err, "could not find test/e2e/credentials.yaml — "+
		"copy test/e2e/credentials.yaml.sample and fill in your evroc credentials")

	client, err := cloud.NewClientFromYAML(context.Background(), data, nil)
	require.NoError(t, err, "failed to create cloud client from credentials.yaml")
	return client
}

// skipIfNoCredentials skips the test when credentials.yaml is missing.
func skipIfNoCredentials(t *testing.T) {
	t.Helper()
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test — set INTEGRATION_TEST=1 to run")
	}
	for _, p := range credentialsPaths {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Skip("Skipping — test/e2e/credentials.yaml not found")
}

// ---------- Tests ----------

func TestCloudClient_Authentication(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	require.NotNil(t, client, "cloud client should not be nil")
	require.NotNil(t, client.SDKClient(), "SDK client should not be nil")
}

func TestCloudClient_ListVirtualMachines(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vms, err := client.VirtualMachines().List(ctx)
	require.NoError(t, err, "listing virtual machines should not error")
	t.Logf("Found %d virtual machines", len(vms))
	for _, vm := range vms {
		t.Logf("  VM: %s", vm.Metadata.Id)
	}
}

func TestCloudClient_ListDisks(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	disks, err := client.Disks().List(ctx)
	require.NoError(t, err, "listing disks should not error")
	t.Logf("Found %d disks", len(disks))
}

func TestCloudClient_ListPublicIPs(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ips, err := client.PublicIPs().List(ctx)
	require.NoError(t, err, "listing public IPs should not error")
	t.Logf("Found %d public IPs", len(ips))
}

func TestCloudClient_ListSecurityGroups(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sgs, err := client.SecurityGroups().List(ctx)
	require.NoError(t, err, "listing security groups should not error")
	t.Logf("Found %d security groups", len(sgs))
	for _, sg := range sgs {
		ruleCount := 0
		if sg.Spec.Rules != nil {
			ruleCount = len(*sg.Spec.Rules)
		}
		t.Logf("  SG: %s (rules: %d)", sg.Metadata.Id, ruleCount)
	}
}

func TestCloudClient_ListPlacementGroups(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgs, err := client.PlacementGroups().List(ctx)
	require.NoError(t, err, "listing placement groups should not error")
	t.Logf("Found %d placement groups", len(pgs))
}

func TestCloudClient_SecurityGroupLifecycle(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)
	ctx := context.Background()

	sgName := fmt.Sprintf("capi-integ-test-%d", time.Now().Unix())
	t.Logf("Creating security group: %s", sgName)

	tcpProto := networkingtypes.TCP
	rules := []networkingtypes.SecurityGroupSpecRulesItem{
		{
			Direction: networkingtypes.Ingress,
			Protocol:  &tcpProto,
			Port:      int32Ptr(22),
			Remote: struct {
				Address          *networkingtypes.SecurityGroupSpecRulesItemAddress `json:"address,omitempty"`
				SecurityGroupRef *string                                            `json:"securityGroupRef,omitempty"`
				SubnetRef        *string                                            `json:"subnetRef,omitempty"`
				VpcRef           *string                                            `json:"vpcRef,omitempty"`
			}{
				Address: &networkingtypes.SecurityGroupSpecRulesItemAddress{
					IpAddressOrCIDR: "0.0.0.0/0",
				},
			},
		},
	}

	sg, err := client.SecurityGroups().Create(ctx, sgName, rules, nil, "")
	require.NoError(t, err, "creating security group should succeed")
	require.NotNil(t, sg)
	t.Logf("Created security group: %s", sg.Metadata.Id)

	// Wait for ready
	readySG, err := client.SecurityGroups().WaitForReady(ctx, sgName, 2*time.Minute)
	require.NoError(t, err, "security group should become ready")
	assert.Equal(t, sgName, readySG.Metadata.Id)
	t.Logf("Security group ready: %s", readySG.Metadata.Id)

	// Verify exists
	exists, err := client.SecurityGroups().Exists(ctx, sgName)
	require.NoError(t, err)
	assert.True(t, exists)

	// Get
	gotSG, err := client.SecurityGroups().Get(ctx, sgName)
	require.NoError(t, err)
	assert.Equal(t, sgName, gotSG.Metadata.Id)
	require.NotNil(t, gotSG.Spec.Rules)
	assert.GreaterOrEqual(t, len(*gotSG.Spec.Rules), 1)

	// Clean up
	t.Logf("Deleting security group: %s", sgName)
	err = client.SecurityGroups().Delete(ctx, sgName)
	require.NoError(t, err, "deleting security group should succeed")

	err = client.SecurityGroups().WaitForDeleted(ctx, sgName, 2*time.Minute)
	require.NoError(t, err, "security group should be deleted")

	exists, err = client.SecurityGroups().Exists(ctx, sgName)
	require.NoError(t, err)
	assert.False(t, exists)

	t.Logf("Security group lifecycle test passed")
}

func TestCloudClient_NonExistentResources(t *testing.T) {
	skipIfNoCredentials(t)
	client := newCloudClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("VM", func(t *testing.T) {
		exists, err := client.VirtualMachines().Exists(ctx, "capi-nonexistent-vm-99999")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Disk", func(t *testing.T) {
		exists, err := client.Disks().Exists(ctx, "capi-nonexistent-disk-99999")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("SecurityGroup", func(t *testing.T) {
		exists, err := client.SecurityGroups().Exists(ctx, "capi-nonexistent-sg-99999")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("PublicIP", func(t *testing.T) {
		exists, err := client.PublicIPs().Exists(ctx, "capi-nonexistent-pip-99999")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestCloudClient_CredentialsFromYAML(t *testing.T) {
	skipIfNoCredentials(t)

	// Verify the credentials.yaml file can be read and parsed directly
	var data []byte
	var err error
	for _, p := range credentialsPaths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)

	client, err := cloud.NewClientFromYAML(context.Background(), data, nil)
	require.NoError(t, err, "NewClientFromYAML should succeed")
	require.NotNil(t, client)

	// Prove authentication works by calling the API
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.VirtualMachines().List(ctx)
	require.NoError(t, err, "listing VMs with YAML-loaded client should work")
	t.Logf("YAML client authentication verified")
}

func int32Ptr(i int32) *int32 {
	return &i
}
