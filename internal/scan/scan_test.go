//go:build darwin

package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathValidatesOptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		options Options
		want    string
	}{
		{name: "negative top", options: Options{Top: -1, Workers: 1}, want: "top must not be negative"},
		{name: "zero workers", options: Options{Workers: 0}, want: "workers must be between"},
		{name: "too many workers", options: Options{Workers: 129}, want: "workers must be between"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Path(t.TempDir(), test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Path() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestPathHandlesSparseHardLinkedAndSymlinkedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(nested, "regular")
	if err := os.WriteFile(regular, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(regular, filepath.Join(nested, "hard-link")); err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(root, "sparse")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1 << 20); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}

	result, err := Path(root, Options{Top: 10, Workers: 2, OneFileSystem: true})
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if result.Files != 2 {
		t.Errorf("Files = %d, want 2", result.Files)
	}
	if result.Directories != 2 {
		t.Errorf("Directories = %d, want 2", result.Directories)
	}
	if result.DuplicateHardLinks != 1 {
		t.Errorf("DuplicateHardLinks = %d, want 1", result.DuplicateHardLinks)
	}
	if result.ApparentBytes < (1<<20)+3 {
		t.Errorf("ApparentBytes = %d, want at least %d", result.ApparentBytes, (1<<20)+3)
	}
	if result.FallbackDirectories != 0 {
		t.Errorf("FallbackDirectories = %d, want 0 on APFS", result.FallbackDirectories)
	}
}

func TestAddEntryTracksAPFSMetrics(t *testing.T) {
	t.Parallel()
	var item Item
	item.addEntry(entry{
		kind:         kindRegular,
		links:        1,
		allocated:    8192,
		private:      4096,
		privateKnown: true,
		extFlags:     extFlagSparse,
	})
	if item.ImmediateReclaimableBytes != 4096 || item.ReclaimableFiles != 1 {
		t.Fatalf("reclaimable metrics = %#v", item)
	}
	if item.CloneFiles != 0 {
		t.Fatalf("CloneFiles = %d, want 0 for an unshared clone ID", item.CloneFiles)
	}
	if item.SparseFiles != 1 {
		t.Fatalf("SparseFiles = %d, want 1", item.SparseFiles)
	}
}

func TestAddEntryDoesNotCallHardLinkedStorageImmediatelyReclaimable(t *testing.T) {
	t.Parallel()
	var item Item
	item.addEntry(entry{
		kind:         kindRegular,
		links:        2,
		allocated:    8192,
		private:      8192,
		privateKnown: true,
	})
	if item.ImmediateReclaimableBytes != 0 || item.ReclaimableFiles != 0 {
		t.Fatalf("hard-linked reclaimable metrics = %#v", item)
	}
}

func TestAddEntrySeparatesConfirmedClonesFromSharedBlockCandidates(t *testing.T) {
	t.Parallel()
	var item Item
	item.addEntry(entry{kind: kindRegular, links: 1, extFlags: extFlagMayShareBlocks})
	item.addEntry(entry{kind: kindRegular, links: 1, extFlags: extFlagSharesAllBlocks})
	if item.SharedBlockFiles != 1 {
		t.Fatalf("SharedBlockFiles = %d, want 1", item.SharedBlockFiles)
	}
	if item.CloneFiles != 1 {
		t.Fatalf("CloneFiles = %d, want 1", item.CloneFiles)
	}
}
