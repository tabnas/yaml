# YAML Test Schema (vendored)

This directory is a vendored copy of part of the
[YAML Test Schema](https://github.com/perlpunk/yaml-test-schema) corpus by
Tina Müller ([@perlpunk](https://github.com/perlpunk)), used under its MIT
licence, which is in [`LICENSE`](LICENSE) beside this file. Thank you to its
author: it is the reference for how YAML's schemas resolve plain and tagged
scalars, and it found real defects here the day it was added.

| File | Source |
|---|---|
| [`schema-core.json`](schema-core.json) | `data/schema-core.json`, byte-identical |
| [`LICENSE`](LICENSE) | `LICENSE`, byte-identical |
| `README.md` | this file, written here (not upstream) |

Upstream commit: `0276b888c6cdaf8e634a5ee79ffd17737d41df2b`
(2024-11-23), from `https://github.com/perlpunk/yaml-test-schema.git`.

## What it measures

`schema-core.json` maps each input, one scalar that is a whole YAML
document, to how the YAML 1.2 **core** schema resolves it:
`[type, value, dumped]`, where `type` is `bool`, `null`, `int`, `float`,
`inf`, `nan` or `str`. Only the core schema's file is vendored, because the
core schema is the one this plugin reads (its YAML 1.1 booleans aside).
The corpus's other schemas (failsafe, JSON, YAML 1.1) live upstream.

Every case is asserted by all three runtimes:
`ts/test/yaml-test-schema.test.ts`, `go/yaml_test_schema_test.go` and
`rs/tests/yaml_test_schema_test.rs`. A case must read exactly as the core
schema says, unless its input is listed in the checked ledger
[`../yaml-test-schema-deviations.tsv`](../yaml-test-schema-deviations.tsv),
which gives the reason for each. A listed input must still read
differently, so a fix fails the test until its line is deleted, and a
census pins the corpus at 245 cases so a truncated copy cannot pass.

## Refreshing

Do not edit the vendored files. To take a newer upstream:

```sh
git clone https://github.com/perlpunk/yaml-test-schema.git /tmp/yts
cp /tmp/yts/data/schema-core.json /tmp/yts/LICENSE test/yaml-test-schema/
git -C /tmp/yts log -1 --format='%H %cs'   # record it above
```

Then run every runtime's suite, update the census if the corpus grew, and
settle each new failure: fix it, or list it in the ledger with a reason.
