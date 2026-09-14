// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"errors"

	evroc "github.com/evroc-oss/evroc-go-sdk"
)

// IsNotFoundError checks if an error indicates a resource was not found.
// Uses the SDK's typed ErrNotFound error for reliable detection.
func IsNotFoundError(err error) bool {
	return errors.Is(err, evroc.ErrNotFound)
}
