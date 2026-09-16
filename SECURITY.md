# Security policy

## Supported versions

Until the first stable release, security fixes are applied to the latest code
on the default branch.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting feature from the repository's
**Security** tab. If private reporting is unavailable, contact the maintainer
through their GitHub profile before disclosing details publicly.

Do not open a public issue containing an exploit, credential, private path,
filesystem report, or other sensitive information. You should receive an
initial response within seven days.

## Scope

Security-sensitive areas include directory traversal, symbolic-link and mount
handling, malformed filesystem metadata, terminal-output escaping, resource
exhaustion, release integrity, and any behavior that could modify user data.

`apfsusage` is currently read-only. A report that depends on adding deletion,
privilege escalation, or bypassing macOS privacy protections is outside the
current product boundary.
