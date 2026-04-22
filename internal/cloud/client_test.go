// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"errors"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
)

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
