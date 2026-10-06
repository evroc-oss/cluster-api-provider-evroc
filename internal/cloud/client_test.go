// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	sdkconfig "github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the real SDK serialization so a custom ref cannot accidentally be
// expanded into a global image ref by WithImage.
func TestDiskCreateImageSource(t *testing.T) {
	const customRef = "/compute/projects/test-project/regions/se-sto/customDiskImages/node-v1"
	for _, tc := range []struct {
		name, image, sourceType, imageRef string
	}{
		{"custom", customRef, "image", customRef},
		{"shorthand", "custom:node-v1", "image", customRef},
		{"stock", "ubuntu.24-04.1", "image", "/compute/global/diskImages/evroc/ubuntu.24-04.1"},
		{"stock ref", "/compute/global/diskImages/evroc/ubuntu.24-04.1", "image", "/compute/global/diskImages/evroc/ubuntu.24-04.1"},
		{"blank", "", "blank", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sdk, err := evroc.New(context.Background(), sdkconfig.Config{
				Auth:    sdkconfig.AuthConfig{Token: "test", ClientID: "test", TokenURL: "https://auth.test/token"},
				API:     sdkconfig.APIConfig{BaseURL: "https://api.test"},
				Context: sdkconfig.ContextConfig{Organization: "test-org", Project: "test-project", Region: "se-sto"},
			}, evroc.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) *http.Response {
				calls++
				assert.Equal(t, http.MethodPost, req.Method)
				assert.True(t, strings.HasSuffix(req.URL.Path, "/projects/test-project/regions/se-sto/disks"), req.URL.Path)
				var body struct {
					Spec struct {
						Source struct {
							Type         string  `json:"type"`
							DiskImageRef *string `json:"diskImageRef"`
						} `json:"source"`
					} `json:"spec"`
				}
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				assert.Equal(t, tc.sourceType, body.Spec.Source.Type)
				if tc.imageRef == "" {
					assert.Nil(t, body.Spec.Source.DiskImageRef)
				} else {
					require.NotNil(t, body.Spec.Source.DiskImageRef)
					assert.Equal(t, tc.imageRef, *body.Spec.Source.DiskImageRef)
				}
				w := httptest.NewRecorder()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"metadata":{"id":"test-disk"}}`))
				return w.Result()
			})}))
			require.NoError(t, err)
			disk, err := (&DiskService{client: sdk}).Create(context.Background(), "test-disk", 50, tc.image, "se-sto-1", nil)
			require.NoError(t, err)
			require.NotNil(t, disk)
			assert.Equal(t, "test-disk", disk.Metadata.Id)
			assert.Equal(t, 1, calls)
		})
	}
}

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
