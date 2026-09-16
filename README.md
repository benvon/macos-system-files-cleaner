# apfsusage

`apfsusage` is a read-only macOS CLI for finding where APFS storage is being
used. It uses Darwin's `getattrlistbulk(2)` interface to retrieve metadata in
batches, avoiding the per-entry `stat` pattern that makes traditional recursive
size tools slow on trees with millions of files.

The project is intentionally discovery-only. It does not delete files, request
elevated privileges, or attempt to reproduce Apple's private **System Data**
classification.

## Requirements

- macOS on Apple Silicon or Intel
- Xcode Command Line Tools
- [mise](https://mise.jdx.dev/) for the pinned development toolchain

Some directories are protected by macOS privacy controls. Inaccessible paths
are reported as incomplete coverage; running as root is neither required nor a
substitute for Full Disk Access.

## Build

```sh
mise install
mise run build
```

The binary is written to `bin/apfsusage`.

## Usage

Inspect volume capacity, Spotlight availability, and local Time Machine
snapshot names:

```sh
bin/apfsusage probe
```

Scan one or more directory trees:

```sh
bin/apfsusage scan /System/Volumes/Data
bin/apfsusage scan "$HOME/Library"
bin/apfsusage scan -top 50 "$HOME/Library/Containers"
```

Produce structured output:

```sh
bin/apfsusage scan -json "$HOME/Library" > report.json
```

Reports contain full paths and may reveal usernames, installed applications,
or document names. Treat them as private data. The repository ignores the
default `report.json` and `reports/` locations to reduce accidental disclosure.

Run `bin/apfsusage scan -h` for all options.

Local development builds report version `dev`. Release builds derive their
version exclusively from the SemVer Git tag that triggered the release and
embed it in the binary:

```sh
bin/apfsusage version
```

## What the numbers mean

- **Allocated bytes** are the filesystem allocation observed for live files.
- **Apparent bytes** are logical file sizes.
- **Immediate free** uses APFS private-size metadata when available. It is a
  conservative per-file estimate, not a prediction for deleting a whole tree.
- **Residual bytes** reconcile a mount-point scan with volume allocation. They
  can include snapshots, other shared APFS volumes, inaccessible content, and
  filesystem metadata; they are not all reclaimable.
- **Coverage counts** make unreadable directories, skipped mounts, fallback
  enumeration, and unsupported private-size measurements visible.

Scans do not follow symbolic links and stay on the starting filesystem by
default. APFS clones, sparse files, purgeable items, File Provider roots, common
macOS packages, and standard user Library collections receive additional labels.
Those labels are evidence, not cleanup recommendations.

The release binaries use the public libSystem `getattrlistbulk` wrapper. Builds
made with `CGO_ENABLED=0` remain functional but use the slower descriptor-relative
fallback and report that fallback in their coverage metrics.

## Development

The canonical local validation entrypoint is:

```sh
mise run ci
```

GitHub Actions invokes the same task. It checks formatting, shell scripts,
workflow and release configuration, `go vet`, race-enabled tests, builds, known Go
vulnerabilities, working-tree secrets, and Git history when a usable commit is
available.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidance and
[docs/research.md](docs/research.md) for the design rationale and limitations.

## Releases

GoReleaser creates Darwin archives for `arm64` and `amd64`, plus SHA-256
checksums and GitHub artifact attestations. Test packaging locally without
publishing:

```sh
mise run release:snapshot
```

Pushing a signed semantic-version tag such as `v0.1.0` runs CI and publishes a
GitHub Release. The binary reports `0.1.0`; there is no separate version file to
keep synchronized. Pre-release tags such as `v0.2.0-rc.1` are supported.

After downloading a release, verify its provenance with the GitHub CLI:

```sh
gh attestation verify --owner benvon apfsusage_*.tar.gz
```

## Security

Please report vulnerabilities according to [SECURITY.md](SECURITY.md). Do not
include private filesystem reports in public issues.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
