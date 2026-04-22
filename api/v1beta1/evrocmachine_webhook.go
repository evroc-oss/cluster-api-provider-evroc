// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package v1beta1

import (
	"context"
	"fmt"
	"regexp"

	"github.com/evroc-oss/evroc-go-sdk/compute"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var evrocmachinelog = logf.Log.WithName("evrocmachine-resource")

var (
	// Compiled regexes for validation
	regionPattern            = regexp.MustCompile(`^[a-z]{2}-[a-z]{3}$`)
	gpuComputeProfilePattern = regexp.MustCompile(`^gn-(l40s|b200)\.`)
)

// EvrocMachineDefaulter implements admission.CustomDefaulter for EvrocMachine.
type EvrocMachineDefaulter struct{}

// EvrocMachineValidator implements admission.CustomValidator for EvrocMachine.
type EvrocMachineValidator struct{}

// SetupEvrocMachineWebhookWithManager sets up the webhook with the Manager.
func SetupEvrocMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&EvrocMachine{}).
		WithDefaulter(&EvrocMachineDefaulter{}).
		WithValidator(&EvrocMachineValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-evrocmachine,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=evrocmachines,verbs=create;update,versions=v1beta1,name=mevrocmachine.kb.io,admissionReviewVersions=v1

// Default implements admission.CustomDefaulter.
func (d *EvrocMachineDefaulter) Default(_ context.Context, obj runtime.Object) error {
	r, ok := obj.(*EvrocMachine)
	if !ok {
		return fmt.Errorf("expected an EvrocMachine but got a %T", obj)
	}

	evrocmachinelog.Info("default", "name", r.Name)

	// Set default region if not specified
	if r.Spec.Region == "" {
		r.Spec.Region = "se-sto"
	}

	// Set default image if not specified
	if r.Spec.Image == "" {
		r.Spec.Image = string(compute.DiskImageUbuntu2204)
	}

	// Default to inheriting cluster security groups so machines are never
	// created without any SGs (which would block all network traffic).
	if r.Spec.NetworkingConfig == nil {
		r.Spec.NetworkingConfig = &MachineNetworkingConfig{}
	}
	if r.Spec.NetworkingConfig.SecurityGroups == nil {
		r.Spec.NetworkingConfig.SecurityGroups = &MachineSecurityGroupsConfig{}
	}
	if !r.Spec.NetworkingConfig.SecurityGroups.InheritFromCluster &&
		len(r.Spec.NetworkingConfig.SecurityGroups.InlineSecurityGroups) == 0 &&
		len(r.Spec.NetworkingConfig.SecurityGroups.ExistingNames) == 0 {
		r.Spec.NetworkingConfig.SecurityGroups.InheritFromCluster = true
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-evrocmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=evrocmachines,verbs=create;update,versions=v1beta1,name=vevrocmachine.kb.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *EvrocMachineValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*EvrocMachine)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocMachine but got a %T", obj)
	}

	evrocmachinelog.Info("validate create", "name", r.Name)

	warnings, errs := r.validateEvrocMachine()
	if len(errs) > 0 {
		return warnings, errs.ToAggregate()
	}
	return warnings, nil
}

// ValidateUpdate implements admission.CustomValidator.
func (v *EvrocMachineValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	r, ok := newObj.(*EvrocMachine)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocMachine but got a %T", newObj)
	}

	evrocmachinelog.Info("validate update", "name", r.Name)

	var allErrs field.ErrorList

	// Validate immutable fields
	oldMachine, ok := oldObj.(*EvrocMachine)
	if !ok {
		return nil, fmt.Errorf("expected an EvrocMachine but got a %T", oldObj)
	}

	// Project is immutable
	if r.Spec.Project != oldMachine.Spec.Project {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "project"),
			"project is immutable",
		))
	}

	// Region is immutable
	if r.Spec.Region != oldMachine.Spec.Region {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "region"),
			"region is immutable",
		))
	}

	// ComputeProfile is immutable (can't resize VMs in-place)
	if r.Spec.ComputeProfile != oldMachine.Spec.ComputeProfile {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "computeProfile"),
			"compute profile is immutable; create a new machine with the desired profile",
		))
	}

	// Image is immutable (baked into the boot disk at creation)
	if r.Spec.Image != oldMachine.Spec.Image {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "image"),
			"image is immutable; create a new machine with the desired image",
		))
	}

	// RootDiskSize is immutable (disk cannot be resized after creation)
	if r.Spec.RootDiskSize != oldMachine.Spec.RootDiskSize {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "rootDiskSize"),
			"root disk size is immutable; create a new machine with the desired size",
		))
	}

	// Run general validation
	warnings, validationErrs := r.validateEvrocMachine()
	allErrs = append(allErrs, validationErrs...)

	if len(allErrs) > 0 {
		return warnings, allErrs.ToAggregate()
	}

	return warnings, nil
}

// ValidateDelete implements admission.CustomValidator.
func (v *EvrocMachineValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateEvrocMachine performs common validation for EvrocMachine.
// Returns field.ErrorList so callers can append individual errors without losing field paths.
func (r *EvrocMachine) validateEvrocMachine() (admission.Warnings, field.ErrorList) {
	var allErrs field.ErrorList
	var warnings admission.Warnings

	// Validate required fields
	if r.Spec.Project == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "project"),
			"project must be specified",
		))
	}

	if r.Spec.Region == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "region"),
			"region must be specified",
		))
	}

	if r.Spec.ComputeProfile == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "computeProfile"),
			"compute profile must be specified",
		))
	}

	if r.Spec.Image == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "image"),
			"image must be specified",
		))
	}

	// Validate compute profile using SDK
	if r.Spec.ComputeProfile != "" && !compute.IsValidVMSize(r.Spec.ComputeProfile) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "computeProfile"),
			r.Spec.ComputeProfile,
			fmt.Sprintf("must be a valid compute profile. Valid options: %s", compute.GetValidVMSizesString()),
		))
	}

	// Validate disk image using SDK
	if r.Spec.Image != "" && !compute.IsValidDiskImage(r.Spec.Image) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "image"),
			r.Spec.Image,
			fmt.Sprintf("must be a valid disk image. Valid options: %s", compute.GetValidDiskImagesString()),
		))
	}

	// Validate region format
	if r.Spec.Region != "" && !regionPattern.MatchString(r.Spec.Region) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "region"),
			r.Spec.Region,
			"region must be in format xx-xxx (e.g., se-sto)",
		))
	}

	// Validate root disk size
	if r.Spec.RootDiskSize < 10 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "rootDiskSize"),
			r.Spec.RootDiskSize,
			"rootDiskSize must be at least 10 GB",
		))
	}

	if r.Spec.RootDiskSize > 10000 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "rootDiskSize"),
			r.Spec.RootDiskSize,
			"rootDiskSize must not exceed 10000 GB (10 TB)",
		))
	}

	// Validate machine name length for derived evroc resource names.
	// The longest built-in suffix is "-boot-disk" or "-public-ip" (10 chars).
	machineResourceOverhead := 10 // len("-boot-disk") or len("-public-ip")
	if len(r.Name)+machineResourceOverhead > evrocMaxResourceNameLen {
		allErrs = append(allErrs, field.TooLong(
			field.NewPath("metadata", "name"),
			r.Name,
			evrocMaxResourceNameLen-machineResourceOverhead,
		))
	}

	// Warn about GPU compute profiles
	if gpuComputeProfilePattern.MatchString(r.Spec.ComputeProfile) {
		warnings = append(warnings, "GPU-enabled compute profiles may have limited availability and higher costs")
	}

	// Warn about small disk sizes for production
	if r.Spec.RootDiskSize < 50 && r.Spec.RootDiskSize >= 10 {
		warnings = append(warnings, "rootDiskSize less than 50 GB is not recommended for production workloads")
	}

	// Validate additionalLabels for evroc compatibility
	if len(r.Spec.AdditionalLabels) > 0 {
		allErrs = append(allErrs, validateAdditionalLabels(r.Spec.AdditionalLabels, field.NewPath("spec", "additionalLabels"))...)
	}

	// Inline config validation: Prevent mixing enabled and existingName
	if r.Spec.NetworkingConfig != nil && r.Spec.NetworkingConfig.PublicIP != nil {
		if r.Spec.NetworkingConfig.PublicIP.Enabled && r.Spec.NetworkingConfig.PublicIP.ExistingName != nil {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "networkingConfig", "publicIP"),
				"cannot specify both enabled (auto-create) and existingName (external)",
			))
		}
	}

	// Validate additional disk name uniqueness and derived name length
	if len(r.Spec.AdditionalDisks) > 0 {
		seenDiskNames := make(map[string]bool)
		for i, disk := range r.Spec.AdditionalDisks {
			diskPath := field.NewPath("spec", "additionalDisks").Index(i)
			if seenDiskNames[disk.Name] {
				allErrs = append(allErrs, field.Duplicate(diskPath.Child("name"), disk.Name))
			}
			seenDiskNames[disk.Name] = true

			// Derived name: "{machine.Name}-{disk.Name}" <= 63
			if len(r.Name) > 0 {
				derivedLen := len(r.Name) + 1 + len(disk.Name)
				if derivedLen > evrocMaxResourceNameLen {
					maxDiskName := evrocMaxResourceNameLen - len(r.Name) - 1
					allErrs = append(allErrs, field.TooLong(diskPath.Child("name"), disk.Name, maxDiskName))
				}
			}
		}
	}

	// Validate inline security group rules, name uniqueness, and derived name length
	if r.Spec.NetworkingConfig != nil && r.Spec.NetworkingConfig.SecurityGroups != nil {
		seenSGNames := make(map[string]bool)
		for i, sg := range r.Spec.NetworkingConfig.SecurityGroups.InlineSecurityGroups {
			sgPath := field.NewPath("spec", "networkingConfig", "securityGroups", "inlineSecurityGroups").Index(i)

			if seenSGNames[sg.Name] {
				allErrs = append(allErrs, field.Duplicate(sgPath.Child("name"), sg.Name))
			}
			seenSGNames[sg.Name] = true

			// Derived name: "{machine.Name}-{sg.Name}" <= 63
			if len(r.Name) > 0 {
				derivedLen := len(r.Name) + 1 + len(sg.Name)
				if derivedLen > evrocMaxResourceNameLen {
					maxSGName := evrocMaxResourceNameLen - len(r.Name) - 1
					allErrs = append(allErrs, field.TooLong(sgPath.Child("name"), sg.Name, maxSGName))
				}
			}

			for j, rule := range sg.Rules {
				rulePath := sgPath.Child("rules").Index(j)
				allErrs = append(allErrs, validateSecurityGroupRule(rule, rulePath)...)
			}
		}

		// Check for duplicates in existingNames, and cross-check against inline names.
		for i, name := range r.Spec.NetworkingConfig.SecurityGroups.ExistingNames {
			sgPath := field.NewPath("spec", "networkingConfig", "securityGroups", "existingNames").Index(i)
			if seenSGNames[name] {
				allErrs = append(allErrs, field.Duplicate(sgPath, name))
			}
			seenSGNames[name] = true
		}
	}

	// Warn control plane machines about missing inheritFromCluster configuration
	if _, isControlPlane := r.Labels["cluster.x-k8s.io/control-plane"]; isControlPlane {
		inheritsFromCluster := r.Spec.NetworkingConfig != nil &&
			r.Spec.NetworkingConfig.SecurityGroups != nil &&
			r.Spec.NetworkingConfig.SecurityGroups.InheritFromCluster

		if !inheritsFromCluster {
			warnings = append(warnings,
				"Control plane machine does not have networkingConfig.securityGroups.inheritFromCluster: true. "+
					"The cluster's controlPlane security groups and public IP (if enabled) will NOT be applied to this VM. "+
					"Add networkingConfig.securityGroups.inheritFromCluster: true to your EvrocMachineTemplate.")
		}
	}

	return warnings, allErrs
}
