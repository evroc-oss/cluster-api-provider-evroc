// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"errors"
	"net/url"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
)

func TestOwnerSelectorRequiresProviderAndImmutableOwner(t *testing.T) {
	values := url.Values{}
	ownerSelector(LabelMachineID, "machine-owner-123").Apply(values)
	assert.Equal(t,
		"capi_managed-by=cluster-api-provider-evroc,capi_machine-id=machine-owner-123",
		values.Get("labelSelector"))
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "SDK not found error",
			err:      evroc.ErrNotFound,
			expected: true,
		},
		{
			name:     "wrapped not found error",
			err:      errors.Join(evroc.ErrNotFound, errors.New("additional context")),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFoundError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVPCRefPassthrough(t *testing.T) {
	// A fully-qualified ref is returned unchanged (no double-wrapping) and does
	// not touch the SDK client, so a nil client is safe here.
	fq := "/networking/projects/p1/regions/se-sto/virtualPrivateClouds/my-vpc"
	assert.Equal(t, fq, VPCRef(nil, fq))
}

func TestSubnetRefPassthrough(t *testing.T) {
	fq := "/networking/projects/p1/regions/se-sto/subnets/my-subnet-a"
	assert.Equal(t, fq, SubnetRef(nil, fq))
}
