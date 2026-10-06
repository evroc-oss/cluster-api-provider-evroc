// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

// Package diskimage normalizes disk image inputs shared by admission and cloud requests.
package diskimage

import "strings"

// StockName converts a canonical stock image reference to the short name expected
// by SDK v0.9.2's WithImage. Other inputs are returned unchanged.
func StockName(image string) string {
	return strings.TrimPrefix(image, "/compute/global/diskImages/evroc/")
}
