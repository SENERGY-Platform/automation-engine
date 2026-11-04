# Third-party code

## goja

- **Source:** https://github.com/dop251/goja, version `v0.0.0-20261011204819-0c9fae1b959d` (the version `go.mod` requires)
- **Where:** `third_party/goja`, which `go.mod` puts in place of `github.com/dop251/goja`
- **Licenses:** MIT (`third_party/goja/LICENSE`); `ftoa` contains code under the Apache License 2.0 from Lucene (`ftoa/LICENSE_LUCENE`) and `ftoa/internal/fast` code under a BSD license from V8 (`ftoa/internal/fast/LICENSE_V8`)
- **Taken over:** 2026-11-04, unchanged except for the patches in `third_party/goja-patches`, which `third_party/goja.sh` applies
