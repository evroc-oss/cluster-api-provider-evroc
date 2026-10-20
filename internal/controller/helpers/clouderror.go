// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package helpers

import (
	"context"
	"errors"
	"fmt"
	"time"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/evroc-oss/cluster-api-provider-evroc/api/v1beta1"
)

// transientRequeue is the retry interval for transient cloud API errors.
const transientRequeue = 30 * time.Second

// retryPolicy controls how controller-runtime should retry after a cloud
// API error.
type retryPolicy int

const (
	// retryAfterDelay means the error is transient (network blip, 5xx).
	// HandleCloudError returns a zero error and a Result with RequeueAfter.
	retryAfterDelay retryPolicy = iota

	// retryWithBackoff means the error is non-retryable until an admin fixes
	// something (quota, permissions). HandleCloudError returns the original
	// error so controller-runtime will use exponential backoff.
	retryWithBackoff

	// terminalNoRetry means the error will never resolve without a spec
	// change (invalid configuration). HandleCloudError wraps it with
	// reconcile.TerminalError so controller-runtime will not requeue.
	terminalNoRetry
)

// cloudErrorClassification holds the result of classifying a cloud API error.
type cloudErrorClassification struct {
	// Reason is a condition reason string (e.g. "QuotaExceeded") suitable for
	// setting on a Ready=False condition.
	Reason string

	// Message is a human-readable failure message.
	Message string

	// Policy tells the caller how controller-runtime should retry.
	Policy retryPolicy
}

// classifyCloudError classifies err for use as a Ready=False condition on
// an EvrocCluster or EvrocMachine. Unclassified errors (including nil) fall
// back to the caller-provided default reason and message.
func classifyCloudError(err error, defaultReason, defaultMessage string) cloudErrorClassification {
	reason, message, policy := defaultReason, defaultMessage, retryAfterDelay

	if evroc.IsQuotaError(err) {
		reason, message, policy = infrav1.QuotaExceededReason, fmt.Sprintf("Quota exceeded: %v", err), retryWithBackoff
	} else if errors.Is(err, evroc.ErrForbidden) {
		reason, message, policy = infrav1.InsufficientPermissionsReason, fmt.Sprintf("Access denied: %v", err), retryWithBackoff
	} else if errors.Is(err, evroc.ErrBadRequest) {
		reason, message, policy = infrav1.InvalidConfigurationReason, fmt.Sprintf("Invalid configuration: %v", err), terminalNoRetry
	}

	return cloudErrorClassification{Reason: reason, Message: message, Policy: policy}
}

// HandleCloudError classifies a cloud API error, sets the failure condition
// on the object via setCondition, emits a warning event, and decides retry
// semantics based on the error class:
//
//   - retryAfterDelay (transient): returns RequeueAfter=30s, nil error.
//   - retryWithBackoff (quota/permissions): returns the original error so
//     controller-runtime applies exponential backoff.
//   - terminalNoRetry (invalid config): returns reconcile.TerminalError(err)
//     so controller-runtime drops the item until the object changes.
//
// Status persistence is owned by the caller.
func HandleCloudError(ctx context.Context, obj client.Object, recorder record.EventRecorder,
	err error, defaultReason, defaultMessage string,
	setCondition func(reason, message string)) (ctrl.Result, error) {

	if err == nil {
		return ctrl.Result{}, nil
	}

	classification := classifyCloudError(err, defaultReason, defaultMessage)
	setCondition(classification.Reason, classification.Message)

	if recorder != nil {
		recorder.Eventf(obj, corev1.EventTypeWarning, classification.Reason, "%s", classification.Message)
	}

	switch classification.Policy {
	case terminalNoRetry:
		log.FromContext(ctx).Error(err, "Terminal cloud API error, will not retry",
			"reason", classification.Reason)
		return ctrl.Result{}, reconcile.TerminalError(err)
	case retryWithBackoff:
		log.FromContext(ctx).Error(err, "Cloud API error, retrying with backoff",
			"reason", classification.Reason)
		return ctrl.Result{}, err
	default: // retryAfterDelay
		log.FromContext(ctx).Info("Transient cloud API error, will retry",
			"error", err, "retryAfter", transientRequeue.String())
		return ctrl.Result{RequeueAfter: transientRequeue}, nil
	}
}
