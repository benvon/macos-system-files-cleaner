package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const maxReportedErrors = 20

// Options controls scan output and resource use.
type Options struct {
	Top           int
	Workers       int
	OneFileSystem bool
}

// Item summarizes one immediate child of a scanned directory, including the
// aggregate metadata for its descendants.
type Item struct {
	Path                      string    `json:"path"`
	AllocatedBytes            uint64    `json:"allocated_bytes"`
	ApparentBytes             uint64    `json:"apparent_bytes"`
	ImmediateReclaimableBytes uint64    `json:"immediate_reclaimable_bytes"`
	ReclaimableFiles          uint64    `json:"reclaimable_files"`
	Files                     uint64    `json:"files"`
	Directories               uint64    `json:"directories"`
	CloneFiles                uint64    `json:"clone_files"`
	SharedBlockFiles          uint64    `json:"shared_block_files"`
	SparseFiles               uint64    `json:"sparse_files"`
	PurgeableItems            uint64    `json:"purgeable_items"`
	SyncRoots                 uint64    `json:"sync_roots"`
	SyntheticItems            uint64    `json:"synthetic_items"`
	Role                      string    `json:"role,omitempty"`
	PackageKind               string    `json:"package_kind,omitempty"`
	Producer                  *Producer `json:"producer,omitempty"`
	Properties                []string  `json:"properties,omitempty"`
}

// Producer describes path-based evidence about the application or service
// responsible for an item. It is not a statement of ownership or safety.
type Producer struct {
	Identifier string `json:"identifier"`
	Confidence string `json:"confidence"`
}

// ScanError records a representative path that could not be inspected.
type ScanError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Volume reconciles live-tree observations with filesystem allocation for a
// scan rooted at a mount point.
type Volume struct {
	Mount          string `json:"mount"`
	TotalBytes     uint64 `json:"total_bytes"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	ResidualBytes  uint64 `json:"residual_bytes"`
}

// Result contains the observable outcome and coverage information for a scan.
type Result struct {
	Path                      string        `json:"path"`
	AllocatedBytes            uint64        `json:"allocated_bytes"`
	ApparentBytes             uint64        `json:"apparent_bytes"`
	ImmediateReclaimableBytes uint64        `json:"immediate_reclaimable_bytes"`
	ReclaimableFiles          uint64        `json:"reclaimable_files"`
	Files                     uint64        `json:"files"`
	Directories               uint64        `json:"directories"`
	CloneFiles                uint64        `json:"clone_files"`
	SharedBlockFiles          uint64        `json:"shared_block_files"`
	SparseFiles               uint64        `json:"sparse_files"`
	PurgeableItems            uint64        `json:"purgeable_items"`
	SyncRoots                 uint64        `json:"sync_roots"`
	SyntheticItems            uint64        `json:"synthetic_items"`
	DuplicateHardLinks        uint64        `json:"duplicate_hard_links"`
	SkippedMounts             uint64        `json:"skipped_mounts"`
	UnreadableDirectories     uint64        `json:"unreadable_directories"`
	FallbackDirectories       uint64        `json:"fallback_directories"`
	Elapsed                   time.Duration `json:"elapsed_ns"`
	Top                       []Item        `json:"top"`
	Errors                    []ScanError   `json:"errors,omitempty"`
	Volume                    *Volume       `json:"volume,omitempty"`
}

type scanner struct {
	options Options
	rootDev uint64

	seenMu sync.Mutex
	seen   map[fileKey]struct{}

	errorMu sync.Mutex
	errors  []ScanError

	duplicateHardLinks    atomic.Uint64
	skippedMounts         atomic.Uint64
	unreadableDirectories atomic.Uint64
	fallbackDirectories   atomic.Uint64
}

type fileKey struct {
	device uint64
	inode  uint64
}

type directoryTask struct {
	top  int
	path string
}

type directoryResult struct {
	top      int
	item     Item
	children []directoryTask
}

// Path scans a directory tree without following symbolic links. It stays on
// the starting filesystem when OneFileSystem is set.
func Path(path string, options Options) (Result, error) {
	if options.Top < 0 {
		return Result{}, fmt.Errorf("top must not be negative")
	}
	if options.Workers < 1 || options.Workers > 128 {
		return Result{}, fmt.Errorf("workers must be between 1 and 128")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Result{}, err
	}
	absolute = filepath.Clean(absolute)

	var rootStat unix.Stat_t
	if err := unix.Lstat(absolute, &rootStat); err != nil {
		return Result{}, err
	}
	if rootStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return Result{}, fmt.Errorf("path is not a directory")
	}
	// Resolve existing symlinks in the caller's starting path once. This handles
	// normal macOS aliases such as /var -> /private/var. Descendants discovered
	// during the scan are still opened with O_NOFOLLOW_ANY to prevent races.
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return Result{}, fmt.Errorf("resolve starting path: %w", err)
	}
	if err := unix.Lstat(absolute, &rootStat); err != nil {
		return Result{}, err
	}

	s := &scanner{
		options: options,
		rootDev: uint64(rootStat.Dev),
		seen:    make(map[fileKey]struct{}),
	}
	started := time.Now()
	entries, fallback, err := readDirectory(absolute)
	if fallback {
		s.fallbackDirectories.Add(1)
	}
	if err != nil {
		return Result{}, err
	}

	items := make([]Item, len(entries))
	queue := make([]directoryTask, 0, len(entries))
	for i, entry := range entries {
		path := filepath.Join(absolute, entry.name)
		items[i].Path = path
		if s.skipEntry(entry) {
			continue
		}
		switch entry.kind {
		case kindDirectory:
			items[i].addEntry(entry)
			queue = append(queue, directoryTask{top: i, path: path})
		case kindRegular:
			if entry.links > 1 && !s.firstLink(entry) {
				s.duplicateHardLinks.Add(1)
				continue
			}
			items[i].addEntry(entry)
		}
	}

	jobs := make(chan directoryTask)
	completed := make(chan directoryResult)
	var workers sync.WaitGroup
	for range options.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				completed <- s.scanDirectory(job)
			}
		}()
	}

	for pending := len(queue); pending > 0; {
		var nextJob chan directoryTask
		var next directoryTask
		if len(queue) > 0 {
			nextJob = jobs
			next = queue[0]
		}
		select {
		case nextJob <- next:
			queue = queue[1:]
		case result := <-completed:
			items[result.top].add(result.item)
			queue = append(queue, result.children...)
			pending += len(result.children) - 1
		}
	}
	close(jobs)
	workers.Wait()
	sort.Slice(s.errors, func(i, j int) bool {
		return s.errors[i].Path < s.errors[j].Path
	})

	var total Item
	for _, item := range items {
		total.add(item)
	}
	for i := range items {
		annotateItem(absolute, &items[i])
		items[i].finalizeProperties()
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AllocatedBytes == items[j].AllocatedBytes {
			return items[i].Path < items[j].Path
		}
		return items[i].AllocatedBytes > items[j].AllocatedBytes
	})
	if options.Top < len(items) {
		items = items[:options.Top]
	}

	result := Result{
		Path:                      absolute,
		AllocatedBytes:            total.AllocatedBytes,
		ApparentBytes:             total.ApparentBytes,
		ImmediateReclaimableBytes: total.ImmediateReclaimableBytes,
		ReclaimableFiles:          total.ReclaimableFiles,
		Files:                     total.Files,
		Directories:               total.Directories + 1,
		CloneFiles:                total.CloneFiles,
		SharedBlockFiles:          total.SharedBlockFiles,
		SparseFiles:               total.SparseFiles,
		PurgeableItems:            total.PurgeableItems,
		SyncRoots:                 total.SyncRoots,
		SyntheticItems:            total.SyntheticItems,
		DuplicateHardLinks:        s.duplicateHardLinks.Load(),
		SkippedMounts:             s.skippedMounts.Load(),
		UnreadableDirectories:     s.unreadableDirectories.Load(),
		FallbackDirectories:       s.fallbackDirectories.Load(),
		Elapsed:                   time.Since(started),
		Top:                       items,
		Errors:                    s.errors,
	}
	result.Volume = volumeReconciliation(absolute, result.AllocatedBytes)
	return result, nil
}

func (s *scanner) scanDirectory(task directoryTask) directoryResult {
	result := directoryResult{top: task.top}
	entries, fallback, err := readDirectory(task.path)
	if fallback {
		s.fallbackDirectories.Add(1)
	}
	if err != nil {
		s.unreadableDirectories.Add(1)
		s.recordError(task.path, err)
		return result
	}

	for _, entry := range entries {
		path := filepath.Join(task.path, entry.name)
		if s.skipEntry(entry) {
			continue
		}
		switch entry.kind {
		case kindDirectory:
			result.item.addEntry(entry)
			result.children = append(result.children, directoryTask{top: task.top, path: path})
		case kindRegular:
			if entry.links > 1 && !s.firstLink(entry) {
				s.duplicateHardLinks.Add(1)
				continue
			}
			result.item.addEntry(entry)
		}
	}
	return result
}

func (s *scanner) skipEntry(entry entry) bool {
	// Opening a mount trigger can initiate network I/O. Skip it before open,
	// even when callers opt into crossing already-mounted filesystems.
	if entry.mount&dirMountStatusTrigger != 0 {
		s.skippedMounts.Add(1)
		return true
	}
	if s.options.OneFileSystem && entry.mount&dirMountStatusMountPoint != 0 {
		s.skippedMounts.Add(1)
		return true
	}
	if s.options.OneFileSystem && entry.device != 0 && entry.device != s.rootDev {
		s.skippedMounts.Add(1)
		return true
	}
	return false
}

func (s *scanner) firstLink(entry entry) bool {
	key := fileKey{device: entry.device, inode: entry.inode}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if _, exists := s.seen[key]; exists {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}

func (s *scanner) recordError(path string, err error) {
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	if len(s.errors) >= maxReportedErrors {
		return
	}
	s.errors = append(s.errors, ScanError{Path: path, Error: err.Error()})
}

func (item *Item) add(other Item) {
	item.AllocatedBytes += other.AllocatedBytes
	item.ApparentBytes += other.ApparentBytes
	item.ImmediateReclaimableBytes += other.ImmediateReclaimableBytes
	item.ReclaimableFiles += other.ReclaimableFiles
	item.Files += other.Files
	item.Directories += other.Directories
	item.CloneFiles += other.CloneFiles
	item.SharedBlockFiles += other.SharedBlockFiles
	item.SparseFiles += other.SparseFiles
	item.PurgeableItems += other.PurgeableItems
	item.SyncRoots += other.SyncRoots
	item.SyntheticItems += other.SyntheticItems
}

func (item *Item) addEntry(entry entry) {
	item.AllocatedBytes += entry.allocated
	item.ApparentBytes += entry.apparent
	switch entry.kind {
	case kindDirectory:
		item.Directories++
	case kindRegular:
		item.Files++
		// Private size describes storage unique to the file object. A single
		// pathname deletion cannot reclaim it while another hard link remains.
		if entry.privateKnown && entry.links == 1 {
			item.ImmediateReclaimableBytes += entry.private
			item.ReclaimableFiles++
		}
		if entry.cloneRefs > 1 || entry.extFlags&extFlagSharesAllBlocks != 0 {
			item.CloneFiles++
		}
		if entry.extFlags&extFlagMayShareBlocks != 0 {
			item.SharedBlockFiles++
		}
		if entry.extFlags&extFlagSparse != 0 {
			item.SparseFiles++
		}
	}
	if entry.extFlags&extFlagPurgeable != 0 {
		item.PurgeableItems++
	}
	if entry.extFlags&extFlagSyncRoot != 0 {
		item.SyncRoots++
	}
	if entry.extFlags&extFlagSynthetic != 0 {
		item.SyntheticItems++
	}
}

func volumeReconciliation(path string, observed uint64) *Volume {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil
	}
	mount := cString(stat.Mntonname[:])
	if filepath.Clean(mount) != path {
		return nil
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	free := stat.Bfree * blockSize
	allocated := total - free
	residual := uint64(0)
	if allocated > observed {
		residual = allocated - observed
	}
	return &Volume{
		Mount:          mount,
		TotalBytes:     total,
		AllocatedBytes: allocated,
		FreeBytes:      free,
		ResidualBytes:  residual,
	}
}

func cString(value []byte) string {
	for i, b := range value {
		if b == 0 {
			return string(value[:i])
		}
	}
	return string(value)
}

func fallbackDirectory(fd int) ([]entry, error) {
	duplicate, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(duplicate), "directory")
	if directory == nil {
		unix.Close(duplicate)
		return nil, fmt.Errorf("create directory handle")
	}
	defer directory.Close()

	directoryEntries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	entries := make([]entry, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		var stat unix.Stat_t
		if err := unix.Fstatat(fd, directoryEntry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("inspect directory entry %q: %w", directoryEntry.Name(), err)
		}
		item := entry{
			name:      directoryEntry.Name(),
			device:    uint64(stat.Dev),
			inode:     stat.Ino,
			links:     uint32(stat.Nlink),
			apparent:  uint64(max(stat.Size, 0)),
			allocated: uint64(max(stat.Blocks, 0)) * 512,
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			item.kind = kindDirectory
		case unix.S_IFREG:
			item.kind = kindRegular
		case unix.S_IFLNK:
			item.kind = kindSymlink
		}
		entries = append(entries, item)
	}
	return entries, nil
}
