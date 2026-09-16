//go:build darwin

package scan

import "testing"

func TestAnnotateContainer(t *testing.T) {
	t.Parallel()
	item := Item{Path: "/Users/example/Library/Containers/com.example.Editor"}
	annotateItem("/Users/example/Library/Containers", &item)
	if item.Role != "app-sandbox-container" || item.Producer == nil {
		t.Fatalf("annotateItem() = %#v", item)
	}
	if item.Producer.Identifier != "com.example.Editor" || item.Producer.Confidence != "high" {
		t.Fatalf("Producer = %#v", item.Producer)
	}
}

func TestAnnotateOpaqueContainerUsesLowConfidence(t *testing.T) {
	t.Parallel()
	item := Item{Path: "/Users/example/Library/Containers/12345678-1234"}
	annotateItem("/Users/example/Library/Containers", &item)
	if item.Producer == nil || item.Producer.Confidence != "low" {
		t.Fatalf("Producer = %#v", item.Producer)
	}
}

func TestAnnotatePackage(t *testing.T) {
	t.Parallel()
	item := Item{Path: "/Users/example/VMs/Test.utm"}
	annotateItem("/Users/example/VMs", &item)
	if item.PackageKind != "utm-vm" {
		t.Fatalf("PackageKind = %q", item.PackageKind)
	}
}

func TestFinalizeProperties(t *testing.T) {
	t.Parallel()
	item := Item{CloneFiles: 2, SparseFiles: 1, SyncRoots: 1}
	item.finalizeProperties()
	want := []string{"apfs-clone", "sparse", "sync-root"}
	if len(item.Properties) != len(want) {
		t.Fatalf("Properties = %#v", item.Properties)
	}
	for i := range want {
		if item.Properties[i] != want[i] {
			t.Fatalf("Properties = %#v", item.Properties)
		}
	}
}
