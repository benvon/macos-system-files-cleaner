# Contributing

Thank you for helping improve `apfsusage`.

## Development setup

This project targets macOS and relies on Darwin-specific filesystem APIs.

```sh
mise install
mise run ci
```

`mise run ci` is the same entrypoint used by GitHub Actions. Please run it before submitting a pull request.

## Design constraints

- Keep discovery read-only. Cleanup behavior requires a separate design and explicit safety review.
- Do not bypass System Integrity Protection, TCC, or other macOS controls.
- Do not follow symbolic links or activate automount triggers while scanning.
- Surface incomplete, unsupported, or stale observations instead of silently treating them as zero.
- Treat paths and generated JSON reports as sensitive user data.
- Prefer documented Apple interfaces and small, maintained dependencies.
- Keep the inventory, attribution, and any future policy layers separate.

## Changes

- Add tests for normal behavior, malformed kernel records, boundary cases, and filesystem safety properties.
- Keep pull requests focused and use Conventional Commit titles.
- Update user-facing documentation when output or interpretation changes.
- Do not hard-wrap Markdown prose. Each source line should end only at a paragraph boundary or an intentional Markdown structure boundary.
- Do not include reports from a real home directory, credentials, tokens, or other private data in tests, issues, or pull requests.

By contributing, you agree that your contributions are licensed under the Apache License 2.0.
