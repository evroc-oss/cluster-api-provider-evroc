// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityGroupsEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{
			name:     "both empty",
			a:        []string{},
			b:        []string{},
			expected: true,
		},
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "same order",
			a:        []string{"sg-1", "sg-2"},
			b:        []string{"sg-1", "sg-2"},
			expected: true,
		},
		{
			name:     "different order",
			a:        []string{"sg-2", "sg-1"},
			b:        []string{"sg-1", "sg-2"},
			expected: true,
		},
		{
			name:     "different length",
			a:        []string{"sg-1"},
			b:        []string{"sg-1", "sg-2"},
			expected: false,
		},
		{
			name:     "different elements",
			a:        []string{"sg-1", "sg-2"},
			b:        []string{"sg-1", "sg-3"},
			expected: false,
		},
		{
			name:     "one nil one empty",
			a:        nil,
			b:        []string{},
			expected: true,
		},
		{
			name:     "single element match",
			a:        []string{"sg-1"},
			b:        []string{"sg-1"},
			expected: true,
		},
		{
			name:     "single element mismatch",
			a:        []string{"sg-1"},
			b:        []string{"sg-2"},
			expected: false,
		},
		{
			name:     "three elements different order",
			a:        []string{"sg-3", "sg-1", "sg-2"},
			b:        []string{"sg-1", "sg-2", "sg-3"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := securityGroupsEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSdkOpts(t *testing.T) {
	t.Run("nil metrics manager returns empty options", func(t *testing.T) {
		opts := sdkOpts(nil)
		assert.Empty(t, opts)
	})
}
