# minimatch legacy adapter

`openapi-typescript@7.13.0` still depends on `@redocly/openapi-core@1.x`, which requires the callable
CommonJS export from `minimatch@5.1.9`. That release pulls a vulnerable `brace-expansion` line.

This private build-only package preserves the minimatch 5 callable surface while delegating to the
audited `minimatch@10.2.5` implementation and its patched `brace-expansion@5.0.8`. The package version
uses SemVer build metadata so Redocly's exact `5.1.9` dependency can be deduplicated without an npm
override. `scripts/check-audit.mjs` locks the package source, dependency graph, runtime surface and
OpenAPI-only reachability.
