// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package framework

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	. "github.com/onsi/gomega"
)

// ClusterctlGenerateFromTemplateInput represents the input parameters for generating from a template.
type ClusterctlGenerateFromTemplateInput struct {
	// ClusterName is the name of the cluster.
	ClusterName string

	// TemplatePath is the path to the template.
	TemplatePath string

	// OutputFilePath is the path to the output file.
	OutputFilePath string

	// ClusterCtlBinaryPath is the path to the ClusterCtl binary.
	ClusterCtlBinaryPath string

	// EnvironmentVariables are the environment variables to be set.
	EnvironmentVariables map[string]string
}

// ClusterctlGenerateFromTemplate will generate a cluster definition from a given template.
// This is a fixed version that properly inherits environment variables.
func ClusterctlGenerateFromTemplate(ctx context.Context, input ClusterctlGenerateFromTemplateInput) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for ClusterctlGenerateFromTemplate")
	Expect(input.TemplatePath).To(BeAnExistingFile(), "Invalid argument. input.Template must be an existing file when calling ClusterctlGenerateFromTemplate")
	Expect(input.ClusterCtlBinaryPath).To(BeAnExistingFile(), "Invalid argument. input.ClusterCtlBinaryPath must be an existing file when calling ClusterctlGenerateFromTemplate")
	Expect(input.OutputFilePath).ToNot(BeEmpty(), "Invalid argument. input.OutputFilePath must not be empty when calling ClusterctlGenerateFromTemplate")
	Expect(input.ClusterName).ToNot(BeEmpty(), "Invalid argument. input.ClusterName must not be empty when calling ClusterctlGenerateFromTemplate")

	args := []string{
		"generate",
		"cluster",
		input.ClusterName,
		"--from",
		input.TemplatePath,
		"--target-namespace",
		"default",
	}

	cmd := exec.Command(input.ClusterCtlBinaryPath, args...)

	// IMPORTANT: Start with the current environment to inherit PATH and other critical variables
	cmd.Env = os.Environ()

	if _, ok := input.EnvironmentVariables["CLUSTER_NAME"]; !ok {
		input.EnvironmentVariables["CLUSTER_NAME"] = input.ClusterName
	}

	// Add/override with the provided environment variables
	for name, val := range input.EnvironmentVariables {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", name, val))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed executing clusterctl generate: %s", stderr.String()))

	err = os.WriteFile(input.OutputFilePath, stdout.Bytes(), os.ModePerm)
	Expect(err).NotTo(HaveOccurred(), "Failed writing template to file")
}
