// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"fmt"
	"net/http"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
)

// quotaAPIError constructs a quota-exceeded API error in the shape the evroc
// API returns it: HTTP 403 with the stable InsufficientQuota reason code and
// the human-readable detail in Debug.
func quotaAPIError(debug string) *evroc.APIError {
	return &evroc.APIError{
		StatusCode: http.StatusForbidden,
		Reason:     evroc.ReasonInsufficientQuota,
		Debug:      debug,
	}
}

func TestClassifyCloudError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		defaultReason   string
		defaultMessage  string
		wantReason      string
		wantPolicy      retryPolicy
		wantMsgContains string
	}{
		{
			name:            "nil error",
			err:             nil,
			defaultReason:   "DefaultReason",
			defaultMessage:  "default message",
			wantReason:      "DefaultReason",
			wantPolicy:      retryAfterDelay,
			wantMsgContains: "default message",
		},
		{
			name: "quota exceeded error (VM)",
			err: fmt.Errorf("failed to create virtual machine: %w", quotaAPIError(
				"not enough quota to perform request. Requested additional 4 vCPUs and 16.0GB memory. Only 0 vCPUs (out of 32 in quota) and 192.0GB memory (out of 320GB in quota) available")),
			defaultReason:   "DefaultReason",
			defaultMessage:  "default",
			wantReason:      "QuotaExceeded",
			wantPolicy:      retryWithBackoff,
			wantMsgContains: "Quota exceeded",
		},
		{
			name: "forbidden without quota reason",
			err: fmt.Errorf("access denied to resource: %w", &evroc.APIError{
				StatusCode: http.StatusForbidden,
				Reason:     "Forbidden",
				Debug:      "access denied",
			}),
			defaultReason:   "DefaultReason",
			defaultMessage:  "default",
			wantReason:      "InsufficientPermissions",
			wantPolicy:      retryWithBackoff,
			wantMsgContains: "Access denied",
		},
		{
			name:            "forbidden sentinel still classifies as permissions",
			err:             fmt.Errorf("denied: %w", evroc.ErrForbidden),
			defaultReason:   "DefaultReason",
			defaultMessage:  "default",
			wantReason:      "InsufficientPermissions",
			wantPolicy:      retryWithBackoff,
			wantMsgContains: "Access denied",
		},
		{
			name:            "bad request sentinel",
			err:             fmt.Errorf("invalid: %w", evroc.ErrBadRequest),
			defaultReason:   "DefaultReason",
			defaultMessage:  "default",
			wantReason:      "InvalidConfiguration",
			wantPolicy:      terminalNoRetry,
			wantMsgContains: "Invalid configuration",
		},
		{
			name:            "transient server error",
			err:             fmt.Errorf("server returned 500: internal error"),
			defaultReason:   "MachineOperationFailed",
			defaultMessage:  "Failed to get/create machine: server returned 500: internal error",
			wantReason:      "MachineOperationFailed",
			wantPolicy:      retryAfterDelay,
			wantMsgContains: "Failed to get/create machine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyCloudError(tt.err, tt.defaultReason, tt.defaultMessage)
			assert.Equal(t, tt.wantReason, result.Reason)
			assert.Equal(t, tt.wantPolicy, result.Policy)
			if tt.wantMsgContains != "" {
				assert.Contains(t, result.Message, tt.wantMsgContains)
			}
		})
	}
}
