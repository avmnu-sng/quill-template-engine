# Security Policy

## Supported versions

Security fixes are made against the latest released version and `main`. With the
v1.x line current, fixes land in the newest v1 release; upgrade to the latest
release to receive them. The pre-1.0 `0.x` releases are no longer patched.

| Version | Supported          |
| ------- | ------------------ |
| 1.x     | :white_check_mark: |
| 0.x     | :x:                |

## Reporting a vulnerability

Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.

Instead, use GitHub's private vulnerability reporting: open the repository's
**Security** tab and choose **Report a vulnerability**. This opens a private
advisory visible only to the maintainer. If you cannot use that channel, contact
the maintainer through their GitHub profile and ask for a private disclosure
path before sharing details.

Please include:

- a description of the issue and its impact,
- the affected version or commit,
- a minimal template or Go reproduction, and
- any proposed mitigation you are aware of.

You can expect an acknowledgement within a few days. Once the report is
confirmed, a fix and a coordinated disclosure timeline will be agreed with you,
and you will be credited in the advisory unless you prefer to remain anonymous.

## Scope

Quill renders templates, including potentially untrusted ones. The sandbox
(`sandbox.Policy`) is the mechanism for constraining what an untrusted template
may do. It restricts the permitted tags, filters, functions, per-type methods,
and per-type properties, and each violation raises a catchable `*errors.Security`.
Reports about sandbox escapes, denial-of-service through crafted templates, or
incorrect escaping under an active strategy are in scope.

Dependency vulnerabilities in the runtime are unlikely: the engine depends only
on the Go standard library. `govulncheck` runs in CI, and Dependabot keeps the
GitHub Actions and any tooling dependencies current.
