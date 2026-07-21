// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var evrocclusterlog = logf.Log.WithName("evroccluster-resource")

// EvrocClusterDefaulter implements admission.CustomDefaulter for EvrocCluster.
type EvrocClusterDefaulter struct{}

// EvrocClusterValidator implements admission.CustomValidator for EvrocCluster.
type EvrocClusterValidator struct{}

// SetupEvrocClusterWebhookWithManager sets up the webhook with the Manager.
func SetupEvrocClusterWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&EvrocCluster{}).
		WithDefaulter(&EvrocClusterDefaulter{}).
		WithValidator(&EvrocClusterValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-evroccluster,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=evrocclusters,verbs=create;update,versions=v1beta1,name=mevroccluster.kb.io,admissionReviewVersions=v1

// Default implements admission.CustomDefaulter.
func (d *EvrocClusterDefaulter) Default(_ context.Context, obj runtime.Object) error {
	r, ok := obj.(*EvrocCluster)
	if !ok {
		return fmt.Errorf("expected an EvrocCluster but got a %T", obj)
	}

	evrocclusterlog.Info("default", "name", r.Name)

	// Set default region if not specified
	if r.Spec.Region == "" {
		r.Spec.Region = "se-sto"
	}

	// Set default failure domains if not specified
	if len(r.Spec.FailureDomains) == 0 {
		r.Spec.FailureDomains = []string{
			"a",
			"b",
			"c",
		}
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-evroccluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=evrocclusters,verbs=create;update,versions=v1beta1,name=vevroccluster.kb.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *EvrocClusterValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*EvrocCluster)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocCluster but got a %T", obj)
	}

	evrocclusterlog.Info("validate create", "name", r.Name)

	return r.validateEvrocCluster()
}

// ValidateUpdate implements admission.CustomValidator.
func (v *EvrocClusterValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	r, ok := newObj.(*EvrocCluster)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocCluster but got a %T", newObj)
	}

	evrocclusterlog.Info("validate update", "name", r.Name)

	var allErrs field.ErrorList

	// Validate immutable fields
	oldCluster, ok := oldObj.(*EvrocCluster)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocCluster but got a %T", oldObj)
	}

	// Project is immutable
	if r.Spec.Project != oldCluster.Spec.Project {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "project"),
			"project is immutable",
		))
	}

	// Region is immutable
	if r.Spec.Region != oldCluster.Spec.Region {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "region"),
			"region is immutable",
		))
	}

	// ControlPlaneEndpoint is immutable once set (check both host and port)
	oldEP := oldCluster.Spec.ControlPlaneEndpoint
	newEP := r.Spec.ControlPlaneEndpoint
	if (oldEP.Host != "" || oldEP.Port != 0) && (newEP.Host != oldEP.Host || newEP.Port != oldEP.Port) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "controlPlaneEndpoint"),
			"controlPlaneEndpoint is immutable once set",
		))
	}

	// FailureDomains: block removal of existing zones. Adding zones is fine,
	// but removing one that may already host VMs would orphan those machines.
	newZones := make(map[string]bool, len(r.Spec.FailureDomains))
	for _, z := range r.Spec.FailureDomains {
		newZones[z] = true
	}
	for _, z := range oldCluster.Spec.FailureDomains {
		if !newZones[z] {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "failureDomains"),
				fmt.Sprintf("cannot remove failure domain %q: existing VMs may be running in it", z),
			))
		}
	}

	// Run general validation
	warnings, err := r.validateEvrocCluster()
	if err != nil {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec"),
			r.Spec,
			err.Error(),
		))
	}

	if len(allErrs) > 0 {
		return warnings, allErrs.ToAggregate()
	}

	return warnings, nil
}

// ValidateDelete implements admission.CustomValidator.
func (v *EvrocClusterValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateEvrocCluster performs common validation for EvrocCluster
func (c *EvrocCluster) validateEvrocCluster() (admission.Warnings, error) {
	var allErrs field.ErrorList
	var warnings admission.Warnings

	// Validate required fields
	if c.Spec.Project == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "project"),
			"project must be specified",
		))
	}

	if c.Spec.Region == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "region"),
			"region must be specified",
		))
	}

	// Every cluster must name the credentials it uses. The CRD schema rejects an
	// absent credentialsRef, but a present-but-empty name still reaches here.
	if c.Spec.CredentialsRef == nil {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "credentialsRef"),
			"credentialsRef must be specified",
		))
	} else if c.Spec.CredentialsRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "credentialsRef", "name"),
			"credentialsRef.name must be specified",
		))
	}

	// Validate region format
	regionPattern := regexp.MustCompile(`^[a-z]{2}-[a-z]{3}$`)
	if c.Spec.Region != "" && !regionPattern.MatchString(c.Spec.Region) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "region"),
			c.Spec.Region,
			"region must be in format xx-xxx (e.g., se-sto)",
		))
	}

	// Validate controlPlaneEndpoint: if partially set, reject it.
	ep := c.Spec.ControlPlaneEndpoint
	if (ep.Host != "" && ep.Port == 0) || (ep.Host == "" && ep.Port != 0) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "controlPlaneEndpoint"),
			ep,
			"host and port must both be set or both be empty",
		))
	}

	// Validate failure domains
	if len(c.Spec.FailureDomains) == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "failureDomains"),
			"at least one failure domain must be specified",
		))
	}

	// Validate each failure domain format
	// Evroc uses simple zone names: a, b, c
	zonePattern := regexp.MustCompile(`^[a-c]$`)
	seenZones := make(map[string]bool)
	for i, zone := range c.Spec.FailureDomains {
		// Check format
		if !zonePattern.MatchString(zone) {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "failureDomains").Index(i),
				zone,
				"failure domain must be a, b, or c",
			))
			continue
		}

		// Check for duplicates
		if seenZones[zone] {
			allErrs = append(allErrs, field.Duplicate(
				field.NewPath("spec", "failureDomains").Index(i),
				zone,
			))
			continue
		}
		seenZones[zone] = true
	}

	// Warn about single failure domain (no HA)
	if len(c.Spec.FailureDomains) == 1 {
		warnings = append(warnings, "using a single failure domain provides no high availability; consider using multiple zones for production clusters")
	}

	// Recommend at least 3 zones for production HA
	if len(c.Spec.FailureDomains) == 2 {
		warnings = append(warnings, "using 2 failure domains may not provide optimal high availability; consider using 3 zones for production clusters")
	}

	// Validate cluster name length for derived evroc resource names.
	// Cluster resources use the prefix "{name}-{uid[:8]}" (name + 9 chars).
	// The longest built-in suffix is "-cp-ip" (6 chars), so: name + 15 <= 63.
	clusterResourceOverhead := 9 + 6 // "-{uid[:8]}" + "-cp-ip"
	if len(c.Name)+clusterResourceOverhead > evrocMaxResourceNameLen {
		allErrs = append(allErrs, field.TooLong(
			field.NewPath("metadata", "name"),
			c.Name,
			evrocMaxResourceNameLen-clusterResourceOverhead,
		))
	}

	// Validate additionalLabels for evroc compatibility
	if len(c.Spec.AdditionalLabels) > 0 {
		allErrs = append(allErrs, validateAdditionalLabels(c.Spec.AdditionalLabels, field.NewPath("spec", "additionalLabels"))...)
	}

	// Validate security group sections (common, controlPlane, worker)
	if c.Spec.SecurityGroups != nil {
		seenSGNames := make(map[string]bool) // names must be unique across ALL sections

		sgSections := []struct {
			name   string
			config *SecurityGroupsConfig
		}{
			{"common", c.Spec.SecurityGroups.Common},
			{"controlPlane", c.Spec.SecurityGroups.ControlPlane},
			{"worker", c.Spec.SecurityGroups.Worker},
		}

		for _, section := range sgSections {
			if section.config == nil {
				continue
			}
			for i, sg := range section.config.InlineSecurityGroups {
				sgPath := field.NewPath("spec", "securityGroups", section.name, "inlineSecurityGroups").Index(i)

				if seenSGNames[sg.Name] {
					allErrs = append(allErrs, field.Duplicate(sgPath.Child("name"),
						fmt.Sprintf("%s (SG names must be unique across all sections)", sg.Name)))
				}
				seenSGNames[sg.Name] = true

				derivedLen := len(c.Name) + 9 + 1 + len(sg.Name)
				if derivedLen > evrocMaxResourceNameLen {
					maxSGName := evrocMaxResourceNameLen - len(c.Name) - 10
					allErrs = append(allErrs, field.TooLong(sgPath.Child("name"), sg.Name, maxSGName))
				}

				for j, rule := range sg.Rules {
					rulePath := sgPath.Child("rules").Index(j)
					allErrs = append(allErrs, validateSecurityGroupRule(rule, rulePath)...)
				}
			}

			// Check for duplicates in existingIDs, and cross-check against inline names.
			for i, name := range section.config.ExistingIDs {
				sgPath := field.NewPath("spec", "securityGroups", section.name, "existingIDs").Index(i)
				if seenSGNames[name] {
					allErrs = append(allErrs, field.Duplicate(sgPath,
						fmt.Sprintf("%s (SG names must be unique across all sections)", name)))
				}
				seenSGNames[name] = true
			}
		}
	}

	if len(allErrs) > 0 {
		return warnings, allErrs.ToAggregate()
	}

	return warnings, nil
}

// validateSecurityGroupRule validates a single security group rule.
func validateSecurityGroupRule(rule SecurityGroupRule, path *field.Path) field.ErrorList {
	var errs field.ErrorList

	if rule.RemoteCIDR != "" && rule.RemoteSecurityGroup != "" {
		errs = append(errs, field.Forbidden(path,
			"remoteCIDR and remoteSecurityGroup are mutually exclusive"))
	}

	if rule.Port != nil && (*rule.Port < 0 || *rule.Port > 65535) {
		errs = append(errs, field.Invalid(path.Child("port"), *rule.Port,
			"port must be between 0 and 65535"))
	}

	if rule.EndPort != nil && (*rule.EndPort < 0 || *rule.EndPort > 65535) {
		errs = append(errs, field.Invalid(path.Child("endPort"), *rule.EndPort,
			"endPort must be between 0 and 65535"))
	}

	if rule.Port != nil && rule.EndPort != nil && *rule.EndPort < *rule.Port {
		errs = append(errs, field.Invalid(path.Child("endPort"), *rule.EndPort,
			"endPort must be greater than or equal to port"))
	}

	return errs
}

// evrocMaxResourceNameLen is the maximum length of an evroc cloud resource name.
const evrocMaxResourceNameLen = 63

// validateAdditionalLabels validates label keys and values for evroc cloud resources.
// evroc does not allow "/" in label keys. Keys and values must also respect length limits.
func validateAdditionalLabels(labels map[string]string, fldPath *field.Path) field.ErrorList {
	var errs field.ErrorList
	for k, v := range labels {
		keyPath := fldPath.Key(k)
		if len(k) == 0 {
			errs = append(errs, field.Required(keyPath, "label key must not be empty"))
		}
		if len(k) > 253 {
			errs = append(errs, field.TooLong(keyPath, k, 253))
		}
		if strings.Contains(k, "/") {
			errs = append(errs, field.Invalid(keyPath, k,
				"evroc does not allow '/' in label keys; use '_' as separator instead"))
		}
		if len(v) > 63 {
			errs = append(errs, field.TooLong(keyPath, v, 63))
		}
	}
	return errs
}
