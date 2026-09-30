// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 evroc

package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evroc "github.com/evroc-oss/evroc-go-sdk"
	sdkconfig "github.com/evroc-oss/evroc-go-sdk/config"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

// fakeCloud serves list (by label selector), get and delete for any collection
// under the region path, keyed "collection/name".
type fakeCloud struct {
	objects  map[string]map[string]any
	failPath string // requests whose path contains this get a 403
}

func newFakeCloud() *fakeCloud { return &fakeCloud{objects: map[string]map[string]any{}} }

func (f *fakeCloud) add(collection, name string, labels map[string]string, spec map[string]any) {
	f.objects[collection+"/"+name] = map[string]any{"metadata": map[string]any{"id": name, "userLabels": labels}, "spec": spec}
}

func (f *fakeCloud) addOwned(collection, name, owner string) {
	f.add(collection, name, map[string]string{"managed-by": owner}, map[string]any{})
}

func (f *fakeCloud) sdk(t *testing.T) *evroc.Client {
	t.Helper()
	sdk, err := evroc.New(context.Background(), sdkconfig.Config{
		Auth:    sdkconfig.AuthConfig{Token: "test", ClientID: "test", TokenURL: "https://auth.test/token"},
		API:     sdkconfig.APIConfig{BaseURL: "https://api.test"},
		Context: sdkconfig.ContextConfig{Organization: "test-org", Project: "test-project", Region: "se-sto"},
	}, evroc.WithHTTPClient(&http.Client{Transport: roundTripFunc(f.handle)}))
	require.NoError(t, err)
	return sdk
}

// handle serves the regional evroc API the cleanup code talks to. Paths end in
// either "<collection>" (list) or "<collection>/<name>" (single resource), and
// the cleanup only ever issues GET and DELETE.
func (f *fakeCloud) handle(req *http.Request) *http.Response {
	w := httptest.NewRecorder()
	key := strings.SplitN(req.URL.Path, "/se-sto/", 2)[1]
	collection, name, isResource := strings.Cut(key, "/")

	switch {
	case f.failPath != "" && strings.Contains(key, f.failPath):
		// Simulated API failure for the error-propagation test.
		w.WriteHeader(http.StatusForbidden)
	case req.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	case req.Method != http.MethodGet:
		w.WriteHeader(http.StatusMethodNotAllowed)
	case isResource:
		// GET <collection>/<name>: used to check whether a VM still exists.
		if item, ok := f.objects[collection+"/"+name]; ok {
			_ = json.NewEncoder(w).Encode(item)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		// GET <collection>?labelSelector=...: the ownership listing.
		items := []any{}
		for id, item := range f.objects {
			if strings.HasPrefix(id, collection+"/") && f.matches(item, req.URL.Query().Get("labelSelector")) {
				items = append(items, item)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}
	return w.Result()
}

func (f *fakeCloud) matches(item map[string]any, selector string) bool {
	labels := item["metadata"].(map[string]any)["userLabels"].(map[string]string)
	for _, kv := range strings.Split(selector, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && labels[k] != v {
			return false
		}
	}
	return true
}

func cleanupUntilDone(t *testing.T, sdk *evroc.Client, deleteDisks bool) int {
	t.Helper()
	for passes := 1; passes <= 10; passes++ {
		pending, err := (&WorkloadResourceService{client: sdk}).Cleanup(context.Background(), "cluster-a", deleteDisks)
		require.NoError(t, err)
		if !pending {
			return passes
		}
	}
	t.Fatal("cleanup never finished")
	return 0
}

var ccmCollections = []string{"loadBalancers", "l4Routes", "backendServices", "backendPools", "publicIPs"}

func TestWorkloadCleanupRemovesOwnedResourcesOnly(t *testing.T) {
	f := newFakeCloud()
	for _, collection := range append(ccmCollections, "disks") {
		f.addOwned(collection, "owned", "cluster-a")
		f.addOwned(collection, "foreign", "cluster-b")
	}
	f.add("hotswapDiskAttachments", "owned", map[string]string{"managed-by": "cluster-a"}, map[string]any{"diskRef": "disks/owned", "virtualMachineRef": "virtualMachines/gone"})

	// Pass 1 issues every delete, pass 2 confirms the attachment is gone and
	// deletes the disk, pass 3 finds nothing.
	require.Equal(t, 3, cleanupUntilDone(t, f.sdk(t), true))
	require.Len(t, f.objects, 6)
	for key := range f.objects {
		require.True(t, strings.HasSuffix(key, "/foreign"), key)
	}
}

func TestWorkloadCleanupRetainsDisksByDefault(t *testing.T) {
	f := newFakeCloud()
	f.addOwned("disks", "owned", "cluster-a")
	cleanupUntilDone(t, f.sdk(t), false)
	require.Contains(t, f.objects, "disks/owned")
}

func TestWorkloadCleanupWaitsForClusterVMs(t *testing.T) {
	f := newFakeCloud()
	f.add("virtualMachines", "node", map[string]string{LabelManagedBy: ManagedByValue, LabelClusterID: "cluster-a"}, map[string]any{})
	f.addOwned("backendPools", "owned", "cluster-a")
	pending, err := (&WorkloadResourceService{client: f.sdk(t)}).Cleanup(context.Background(), "cluster-a", false)
	require.NoError(t, err)
	require.True(t, pending)
	require.Contains(t, f.objects, "backendPools/owned")
}

func TestWorkloadCleanupReturnsAPIErrors(t *testing.T) {
	f := newFakeCloud()
	f.addOwned("backendPools", "owned", "cluster-a")
	f.failPath = "backendPools"
	_, err := (&WorkloadResourceService{client: f.sdk(t)}).Cleanup(context.Background(), "cluster-a", false)
	require.ErrorContains(t, err, "failed to list backendPools")
	require.Contains(t, f.objects, "backendPools/owned")
}
