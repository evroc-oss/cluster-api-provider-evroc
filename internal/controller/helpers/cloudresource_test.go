// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"errors"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	"github.com/stretchr/testify/assert"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		{
			name:   "nil error",
			err:    nil,
			expect: false,
		},
		{
			name:   "SDK ErrNotFound",
			err:    evroc.ErrNotFound,
			expect: true,
		},
		{
			name:   "wrapped ErrNotFound",
			err:    errors.Join(evroc.ErrNotFound, errors.New("additional context")),
			expect: true,
		},
		{
			name:   "other error",
			err:    errors.New("connection refused"),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFoundError(tt.err)
			assert.Equal(t, tt.expect, result)
		})
	}
}
