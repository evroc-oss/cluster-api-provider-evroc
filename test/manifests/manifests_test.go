// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

// Package manifests validates the shipped examples and cluster templates against
// the generated CRD schemas, using the same validator the API server runs. A
// manifest that would be rejected at kubectl apply fails here instead.
package manifests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"sigs.k8s.io/yaml"
)

const crdDir = "../../helm/cluster-api-provider-evroc/crds"

const chartDir = "../../helm/cluster-api-provider-evroc"

// manifestGlobs are the shipped manifests users copy or clusterctl renders.
var manifestGlobs = []string{
	"../../examples/*.yaml",
	"../../templates/cluster-template*.yaml",
}

// templateVars are the values a user supplies. Anything absent falls back to its
// ${VAR:=default}, so the defaults get exercised too.
var templateVars = map[string]string{
	"CLUSTER_NAME":                "test-cluster",
	"NAMESPACE":                   "default",
	"EVROC_PROJECT":               "test-project",
	"EVROC_SSH_KEY":               "ssh-rsa AAAAB3Nza",
	"EVROC_CREDENTIALS_SECRET":    "evroc-credentials",
	"KUBERNETES_VERSION":          "v1.28.0",
	"CONTROL_PLANE_MACHINE_COUNT": "3",
	"WORKER_MACHINE_COUNT":        "3",
}

var varPattern = regexp.MustCompile(`\$\{([A-Z_]+)(?::=([^}]*))?\}`)

// render substitutes ${VAR} and ${VAR:=default} the way clusterctl does.
func render(text string) string {
	return varPattern.ReplaceAllStringFunc(text, func(m string) string {
		g := varPattern.FindStringSubmatch(m)
		if v, ok := templateVars[g[1]]; ok {
			return v
		}
		if g[2] != "" {
			return g[2]
		}
		// No value and no default: clusterctl errors rather than rendering "".
		// Surface that as a marker so validation fails loudly.
		return "<<UNSET:" + g[1] + ">>"
	})
}

// loadValidators builds a schema validator per CRD kind from the generated CRDs.
func loadValidators(t *testing.T) map[string]validation.SchemaValidator {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob CRDs: %v", err)
	}

	validators := map[string]validation.SchemaValidator{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("parse CRD %s: %v", p, err)
		}
		if len(crd.Spec.Versions) == 0 || crd.Spec.Versions[0].Schema == nil {
			t.Fatalf("CRD %s has no schema", p)
		}

		// Convert the v1 schema to the internal type the validator expects.
		internal := &apiextensions.JSONSchemaProps{}
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
			crd.Spec.Versions[0].Schema.OpenAPIV3Schema, internal, nil,
		); err != nil {
			t.Fatalf("convert schema for %s: %v", crd.Spec.Names.Kind, err)
		}

		v, _, err := validation.NewSchemaValidator(internal)
		if err != nil {
			t.Fatalf("build validator for %s: %v", crd.Spec.Names.Kind, err)
		}
		validators[crd.Spec.Names.Kind] = v
	}
	return validators
}

func manifestFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, g := range manifestGlobs {
		matches, err := filepath.Glob(g)
		if err != nil {
			t.Fatalf("glob %s: %v", g, err)
		}
		files = append(files, matches...)
	}
	return files
}

func TestManifestsMatchCRDSchemas(t *testing.T) {
	validators := loadValidators(t)
	if len(validators) == 0 {
		t.Fatalf("no CRDs found in %s — run 'make manifests'", crdDir)
	}

	files := manifestFiles(t)
	if len(files) == 0 {
		t.Fatal("no manifests found to validate")
	}

	checked := 0
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			for _, doc := range strings.Split(render(string(raw)), "\n---") {
				if strings.TrimSpace(doc) == "" {
					continue
				}
				obj := map[string]interface{}{}
				if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
					t.Fatalf("unparseable YAML: %v", err)
				}

				kind, _ := obj["kind"].(string)
				validator, ok := validators[kind]
				if !ok {
					continue // not one of ours (CAPI Cluster, KubeadmConfig, ...)
				}
				checked++

				name := "<unnamed>"
				if md, ok := obj["metadata"].(map[string]interface{}); ok {
					if n, ok := md["name"].(string); ok {
						name = n
					}
				}
				for _, e := range validation.ValidateCustomResource(nil, obj, validator) {
					t.Errorf("%s/%s: %v", kind, name, e)
				}
			}
		})
	}

	if checked == 0 {
		t.Error("no EvrocCluster/EvrocClusterTemplate manifests were validated — check the globs")
	}
	t.Logf("validated %d manifest(s) against %d CRD schema(s)", checked, len(validators))
}

// chartRenderCases are the value permutations to render. `helm lint` does not
// render templates, so it cannot see a bare "volumes:" key whose only entry
// sits behind a conditional — that key renders as null and the API server
// rejects it. Rendering each permutation and parsing the result catches it.
var chartRenderCases = []struct {
	name string
	args []string
}{
	{"defaults", nil},
	{"webhook-enabled", []string{"--set", "webhook.enabled=true"}},
	{"webhook-disabled", []string{"--set", "webhook.enabled=false"}},
	{"with-crds", []string{"--include-crds"}},
	{"replicas-3", []string{"--set", "controller.replicas=3"}},
}

// nullableDeploymentFields are list-valued keys that, if emitted with no
// entries, render as a bare "key:" (null) and are rejected once applied.
var nullableDeploymentFields = struct {
	pod       []string
	container []string
}{
	pod:       []string{"volumes", "initContainers", "imagePullSecrets", "tolerations"},
	container: []string{"volumeMounts", "env", "ports", "args"},
}

// TestChartRendersValidYAML renders the Helm chart across its value
// permutations and asserts no Deployment field is a bare null list key.
func TestChartRendersValidYAML(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not found in PATH; skipping chart render validation")
	}

	for _, tc := range chartRenderCases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"template", "test", chartDir, "--set", "fullnameOverride=test"}, tc.args...)
			out, err := exec.CommandContext(t.Context(), "helm", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("helm template failed: %v\n%s", err, out)
			}

			var sawDeployment bool
			for _, doc := range strings.Split(string(out), "\n---") {
				if strings.TrimSpace(doc) == "" {
					continue
				}
				obj := map[string]interface{}{}
				if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
					t.Fatalf("unparseable rendered YAML: %v", err)
				}
				if kind, _ := obj["kind"].(string); kind != "Deployment" {
					continue
				}
				sawDeployment = true
				checkDeploymentNullFields(t, obj)
			}
			if !sawDeployment {
				t.Error("no Deployment rendered")
			}
		})
	}
}

// checkDeploymentNullFields fails the test for any pod- or container-level list
// key that is present but null (a bare "volumes:" with no entries).
func checkDeploymentNullFields(t *testing.T, deployment map[string]interface{}) {
	t.Helper()
	spec := nestedMap(deployment, "spec", "template", "spec")
	if spec == nil {
		return
	}
	for _, f := range nullableDeploymentFields.pod {
		if v, present := spec[f]; present && v == nil {
			t.Errorf("Deployment .spec.template.spec.%s is null (bare key, no entries)", f)
		}
	}
	containers, _ := spec["containers"].([]interface{})
	for _, c := range containers {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := cm["name"].(string)
		for _, f := range nullableDeploymentFields.container {
			if v, present := cm[f]; present && v == nil {
				t.Errorf("container %s .%s is null (bare key, no entries)", name, f)
			}
		}
	}
}

// nestedMap walks a chain of map keys, returning nil if any hop is missing or
// not a map.
func nestedMap(m map[string]interface{}, keys ...string) map[string]interface{} {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}
