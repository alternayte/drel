# Security Policy

## Supported versions

drel is pre-1.0. Security fixes are applied to the latest `0.x` minor release.

## Reporting a vulnerability

Please **do not** open a public issue for security-sensitive reports. Instead,
use GitHub's private vulnerability reporting
([Security → Report a vulnerability](https://github.com/alternayte/drel/security/advisories/new))
on this repository.

Include a description, affected versions, and a minimal reproduction if possible.
We'll acknowledge the report and work with you on a fix and coordinated
disclosure.

## Scope notes

- Generated and builder queries are fully parameterized, and identifiers are
  quoted/escaped. The raw-SQL escape hatches (`RawQuery`, `Engine.Exec`,
  `Tx.Exec`, raw predicates) pass SQL through to the driver — callers are
  responsible for parameterizing untrusted input there.
- Migration SQL is generated for review and committed to your repository; review
  it before applying to production.
