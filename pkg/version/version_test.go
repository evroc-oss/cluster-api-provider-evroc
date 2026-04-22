// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package version

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	info := Get()

	assert.NotEmpty(t, info.Version)
	assert.NotEmpty(t, info.GitCommit)
	assert.NotEmpty(t, info.BuildDate)
	assert.NotEmpty(t, info.GoVersion)
	assert.NotEmpty(t, info.Platform)
	assert.Equal(t, runtime.Version(), info.GoVersion)
	assert.Contains(t, info.Platform, runtime.GOOS)
	assert.Contains(t, info.Platform, runtime.GOARCH)
}

func TestInfoString(t *testing.T) {
	info := Info{
		Version:   "v1.0.0",
		GitCommit: "abc123",
		BuildDate: "2026-01-01",
		GoVersion: "go1.24",
		Platform:  "linux/amd64",
	}

	result := info.String()

	assert.Contains(t, result, "v1.0.0")
	assert.Contains(t, result, "abc123")
	assert.Contains(t, result, "2026-01-01")
	assert.Contains(t, result, "go1.24")
	assert.Contains(t, result, "linux/amd64")
	assert.True(t, strings.HasPrefix(result, "Version:"))
}
