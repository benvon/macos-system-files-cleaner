# Research: discovering macOS System Data efficiently

Last updated: 2026-09-16

## Executive conclusion

There is no public Apple API for enumerating the exact membership of the System Data bar in System Settings. Apple defines it as a changing residual category: files not assigned to more specific categories, primarily logs, caches, VM/runtime resources, temporary files, fonts, app support files, and plug-ins. A trustworthy tool must say that clearly and reconcile several different views instead of inventing an exact answer.

The recommended discovery stack is:

1. Use volume-capacity APIs for the allocation truth at the APFS container/volume level.
2. Use `getattrlistbulk(2)` for a fast, exact-as-permissions-allow recursive metadata inventory.
3. Use Spotlight only as an optional instant hint/index, never as the authoritative inventory.
4. Inventory local APFS/Time Machine snapshots separately because they retain blocks outside the live namespace.
5. Report allocated bytes, apparent bytes, scan coverage, mount boundaries, hard-link deduplication, clone caveats, and permissions together.

The proof of concept implements a preliminary baseline across these layers: capacity probing, snapshot-name discovery, Spotlight status, coverage reporting, reconciliation, and a bulk metadata scanner. It intentionally has no deletion path.

## Prototype validation

The bulk scanner has been exercised against an APFS Data volume containing
millions of entries. It completed substantially faster than `/usr/bin/du -sk`
in an initial warm-cache comparison while retaining coverage and permission
errors. Those measurements were exploratory rather than a controlled benchmark,
so this repository intentionally does not publish machine-specific performance
or application-usage details as general claims.

A reproducible benchmark should report hardware, macOS version, filesystem,
cache state, dataset characteristics, entry count, inaccessible paths, and
multiple runs. Spotlight must remain optional: it may be disabled, stale,
privacy-filtered, or unavailable for system locations.

## What System Data means

Apple's Storage settings documentation says System Data contains files that do not fall into the named categories. Its examples include logs, caches, VM files, runtime resources, temporary files, fonts, app support files, and plug-ins. The contents are managed by macOS and vary with current system state. This means System Data is a classification result, not a directory tree or an APFS allocation class.

Consequences:

- A scanner can explain large locations but cannot reproduce Apple's number exactly.
- A large developer cache, application container, VM disk, or backup artifact can land in System Data even though a third-party app created it.
- The Storage panel can lag filesystem changes while its categorization process refreshes.
- “System Data” and “safe to delete” are entirely different judgments.

## Why APFS totals do not reconcile like a traditional filesystem

APFS supports space sharing, clones, sparse files, compression, and snapshots. These features break a naive sum of file sizes:

- **Space sharing:** System and Data volumes can report the same container capacity and free-space pool. Their totals must not be added.
- **Logical versus allocated size:** sparse and compressed files may allocate much less than their logical length.
- **Clones:** two paths may refer to shared extents. Summing each path's allocated size can count shared blocks more than once.
- **Snapshots:** blocks no longer referenced by the live directory tree can remain allocated because a snapshot still references them.
- **Purgeable space:** Apple says Time Machine local snapshot space is counted as available and reclaimed automatically as space is needed. A frightening category number is therefore not identical to immediate storage pressure.

The production tool should eventually query Foundation's important-usage and opportunistic-usage capacity values in addition to `statfs`, because those APIs express the system's purgeability policy. They are Required Reason APIs for distributed apps and need the appropriate privacy manifest declaration.

## Enumeration options considered

### `du`, `filepath.Walk`, and ordinary `readdir`/`stat`

These are portable and exact within permissions, but portability is not a requirement. The expensive pattern is listing a directory and then requesting file information individually. It produces roughly one metadata syscall per entry and performs poorly on trees with millions of small files.

Use only as a compatibility fallback when a mounted filesystem rejects bulk attributes. The prototype counts each directory where it falls back.

### Spotlight (`NSMetadataQuery`, `MDQuery`, or `mdfind`)

Spotlight can return indexed results quickly and supports size metadata. It is useful for an immediate “where should I look first?” mode or for incremental monitoring after a baseline scan.

It is not suitable as the source of truth. Apple documents that system directories and some volumes are not indexed and that users can exclude directories and document types. Indexing can also be disabled, stale, or privacy-filtered. On the test machine, `mdutil -s /` reported that the Spotlight server was disabled, demonstrating why the tool needs a non-Spotlight path.
It is not suitable as the source of truth. Apple documents that system
directories and some volumes are not indexed and that users can exclude
directories and document types. Indexing can also be disabled, stale, or
privacy-filtered, so the tool always needs a non-Spotlight path.

### APFS fast directory sizing

Apple advertises fast directory sizing as an APFS capability, but its public file-size resource keys describe individual resources; they do not provide a documented, recursively exact, clone- and snapshot-aware directory total suitable for this tool. Treat Finder's fast folder-size experience as an implementation detail rather than a public contract.

### `getattrlistbulk(2)` — selected

Darwin introduced `getattrlistbulk(2)` in OS X 10.10. One call returns names and requested metadata for many entries in an open directory. We request only the fields needed for discovery: returned-attribute masks, name, vnode type, device, file ID, link count, apparent size, and allocated size.

Advantages:

- one kernel call returns many entries and their sizes;
- no content reads or Spotlight dependency;
- native APFS support and a stable Darwin ABI;
- can avoid symlink traversal and mounted-volume crossings;
- reports per-entry allocation rather than only logical length.

Costs and limitations:

- Go's standard library and `x/sys/unix` expose the Darwin types but not a
  high-level wrapper, so the prototype calls the public libSystem function
  through a small cgo boundary and uses a bounds-checked record decoder;
- builds made without cgo use a slower descriptor-relative fallback instead of
  issuing a raw syscall;
- filesystems such as SMB/NFS may reject the operation and need a slower fallback;
- permission/TCC failures still apply;
- allocated size is not unique physical size for APFS clones;
- the call inventories the live namespace, not snapshot-only blocks.

The scanner requests `ATTR_CMNEXT_PRIVATESIZE` to estimate blocks that would be
freed immediately by deleting an individual file. It reports measurement
coverage and excludes multiply linked files from that estimate. This is not a
complete directory reclaim estimate: blocks shared only among several selected
clones may become reclaimable only when the last clone is removed.

## Open-source component assessment

- **golang.org/x/sys/unix:** selected. It is maintained by the Go project, BSD-3-Clause licensed, and provides the supported Darwin constants, types, and syscall bridge.
- **Mole:** useful prior art and a mature open-source macOS cleaner, but it is GPL-3.0 and its cleanup scope is broader than this discovery engine. We should study behavior and safety cases without importing GPL code unless this project intentionally adopts compatible licensing.
- **cull:** useful, MIT-licensed recent prior art for clone-aware `getattrlistbulk` scanning. It is an application rather than a stable scanner library, so copying its internal package would create an awkward dependency. Its approach supports independently validating this design.
- **dumac:** directly targets fast `du`-style scans, but the repository currently has no detected license and is Rust-based. Do not reuse its code.
- **DiskInventoryX/GrandPerspective/ncdu-style tools:** useful comparison targets, but they do not solve Apple's classification or snapshot reconciliation. GUI-first or portable designs also do not match the intended lightweight Go CLI boundary.

The simplest dependency posture is therefore the Go standard library plus `x/sys/unix`, with Apple-provided command-line tools used only for non-destructive snapshot and index status in the proof of concept.

## Safety and trust boundaries

Even discovery crosses meaningful boundaries: filenames and directory trees are private data, and a full scan can encounter attacker-controlled names, symlinks, mount triggers, network volumes, and rapidly changing directories.

The prototype therefore:

- never deletes, edits, opens file contents, or follows symlinks;
- uses fixed executables and separately supplied arguments for system commands;
- stays on the starting device by default;
- bounds concurrency and retained error samples;
- validates every variable-length kernel record before decoding;
- counts unreadable directories and fallbacks instead of claiming full coverage;
- emits structured JSON without shell interpolation;
- deduplicates multiply linked regular files by device and inode.

Before cleanup is added, the design should require explicit allowlisted rules, ownership/provenance checks, dry-run manifests, age/size thresholds, atomic quarantine to a same-volume staging area where possible, a reversible grace period, and a separate confirmation step. System Integrity Protection and TCC must never be bypassed.

Full Disk Access may be needed for a complete inventory. The tool should detect and explain missing coverage; it should not automatically request elevation or relaunch itself with `sudo`. Root access does not automatically grant every TCC permission and would expand the blast radius of future cleanup.

## Suggested discovery taxonomy

The classifier should remain an explainable rule set, versioned independently of the scanner. Early candidate groups:

- system and per-user caches and logs;
- `/private/var` runtime and temporary data;
- virtual-memory/swap artifacts;
- developer artifacts (Xcode DerivedData, archives, device support, simulator data);
- VM and container disk images (Docker, Podman, Lima, UTM, Parallels, VMware);
- package-manager caches;
- application support and sandbox containers;
- iOS device backups and update files;
- local Time Machine and update snapshots;
- mail/message attachments and photo/music libraries, labeled as Apple separately categorizes them;
- other-user data and files inaccessible to the current process.

Each rule should report producer, evidence, confidence, expected regeneration behavior, and a future cleanup risk level. Discovery output should not call a location disposable solely because its path contains `Cache`.

## Production architecture

Keep three layers separate:

1. **Inventory engine:** Darwin bulk scanner, volume boundaries, hard-link identity, size facts, errors, and timing.
2. **Attribution engine:** explainable rules that classify paths and identify producers without deciding to delete.
3. **Policy/action engine:** future opt-in cleanup plans with preconditions, safety grades, manifests, and rollback behavior.

JSON should be a stable interface from the beginning. It enables fixture tests, comparisons with `du` on bounded trees, before/after snapshots, performance regression tests, and external visualization without bloating the CLI.

## Validation plan

The proof of concept should be tested in this order:

1. Unit-test malformed and valid bulk records.
2. Compare allocated/apparent totals with `du -sk` and `du -skA` on small controlled fixtures containing regular files, sparse files, hard links, symlinks, and unreadable directories.
3. Confirm mount-boundary behavior with a mounted disk image or simulator runtime.
4. Benchmark warm and cold scans against `/usr/bin/du` on representative high-file-count trees; report hardware, OS, cache state, file count, and elapsed time.
5. Compare the live-tree total with APFS volume allocation and document the residual caused by snapshots, clones, inaccessible paths, and filesystem metadata.
6. Only after coverage is understood, add attribution rules and compare their sum to the Storage panel as a heuristic—not an equality assertion.

## Sources

- Apple Support, [Change Storage settings on Mac](https://support.apple.com/guide/mac-help/change-storage-settings-mchl3d437fbc/mac)
- Apple Support, [Free up storage space on Mac](https://support.apple.com/102624)
- Apple Support, [About Time Machine local snapshots](https://support.apple.com/102154)
- Apple Developer, [About Apple File System](https://developer.apple.com/documentation/foundation/about-apple-file-system)
- Apple Developer, [getattrlist(2) manual](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/getattrlist.2)
- Apple Open Source, [getattrlistbulk(2) manual](https://github.com/apple/darwin-xnu/blob/main/bsd/man/man2/getattrlistbulk.2)
- Apple Developer, [NSMetadataQuery](https://developer.apple.com/documentation/foundation/nsmetadataquery)
- Apple Developer Archive, [Searching File Metadata with NSMetadataQuery](https://developer.apple.com/library/archive/documentation/Carbon/Conceptual/SpotlightQuery/Concepts/QueryingMetadata.html)
- Apple Developer, [Checking Volume Storage Capacity](https://developer.apple.com/documentation/foundation/checking-volume-storage-capacity)
- Apple Developer, [URLResourceKey.totalFileAllocatedSizeKey](https://developer.apple.com/documentation/foundation/urlresourcekey/totalfileallocatedsizekey)
- Go project, [golang.org/x/sys/unix](https://pkg.go.dev/golang.org/x/sys/unix)
- tw93, [Mole](https://github.com/tw93/Mole)
- legostin, [cull](https://github.com/legostin/cull)
- healeycodes, [dumac](https://github.com/healeycodes/dumac)
