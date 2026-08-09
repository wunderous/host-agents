package ops

import (
	"reflect"
	"testing"
)

func TestValidateOciStoragePolicy(t *testing.T) {
	if err := validateOciStoragePolicy(defaultOciStoragePolicy()); err != nil {
		t.Fatalf("default policy should validate: %v", err)
	}
	for name, policy := range map[string]ociStoragePolicy{
		"negative budget":  {MaxBytes: -1, MinAgeSeconds: defaultOciStorageMinAgeSeconds},
		"too small budget": {MaxBytes: minOciStorageBudgetBytes - 1, MinAgeSeconds: defaultOciStorageMinAgeSeconds},
		"too young":        {MaxBytes: 0, MinAgeSeconds: minOciStorageMinAgeSeconds - 1},
		"too old":          {MaxBytes: 0, MinAgeSeconds: maxOciStorageMinAgeSeconds + 1},
	} {
		if err := validateOciStoragePolicy(policy); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestOciStoragePolicyPersistsAtomically(t *testing.T) {
	path := t.TempDir() + "/oci-storage-policy.json"
	service := &HostOperationsService{ociStoragePolicyPath: path}
	want := ociStoragePolicy{MaxBytes: 8 << 30, MinAgeSeconds: 3 * 24 * 60 * 60}
	if err := service.saveOciStoragePolicy(want); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	got, err := service.loadOciStoragePolicy()
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded policy = %#v, want %#v", got, want)
	}
}

func TestSelectOciPruneCandidates(t *testing.T) {
	cutoff := int64(1000)
	got := selectOciPruneCandidates([]podmanImage{
		{ID: "young", Created: 1200},
		{ID: "old-2", Created: 700},
		{ID: "active", Created: 600, Containers: 1},
		{ID: "old-1", Created: 500},
		{ID: "missing-time", Created: 0},
	}, cutoff)
	want := []podmanImage{{ID: "old-1", Created: 500}, {ID: "old-2", Created: 700}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}
