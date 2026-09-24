# ci/

Staging area for GitHub Actions workflow changes.

This directory exists because session credentials cannot write
`.github/workflows/*` — see admin `DECISIONS.md` ADR-8. To change CI:

1. Put the intended workflow file in `workflows/`.
2. A maintainer promotes it with the admin `rollout/apply-ci-folders.sh`
   script.

## Pending

Nothing.

## Promoted

Both of these were staged here and now run from `.github/workflows/`:

- **`rust.yml`**, the Rust gate: `rs/` built, tested,
  `rustfmt`-checked and clippy-clean at `-D warnings`. The commands live
  in `ci/rust/run.sh`, which the workflow calls and you can run too;
  `make test-rs` is the fast inner loop.

  It is standalone rather than an arm of `ci.yml`, because `ci.yml`
  calls the org-shared polyglot workflow, which takes no Rust input, so
  the Rust port is gated without changing `tabnas/.github`. The job
  clones `parser`, `jsonic`, `json` and `support` beside the checkout:
  `rs/Cargo.toml` resolves all four as path dependencies on siblings,
  and none of them is published.

  Its `paths` lists name the grammar and its embedder as well as `rs/`,
  because `rs/src/lib.rs` carries a generated copy of
  `yaml-grammar.jsonic` and `rs/tests/grammar_test.rs` compares the two.

  The lock check is deliberately not blanket `--locked`, which would
  turn a sibling's release into a red build on every pull request here,
  including ones touching no Rust. See the comment in `ci/rust/run.sh`.

- **`docs.yml`** — the prose gate: Vale over the reader-facing pages at
  the levels set in `.vale.ini`, on the file list
  `ts/scripts/gated-docs.cjs` produces. See `docs/STYLE-GUIDE.md`.

  It needs no sibling checkouts and no secrets, and pins its own Vale
  version. Errors fail the job; warnings go to the run summary as a
  report. `make prose` runs the identical check locally, and the test
  suite runs the other half of the gate (`ts/test/docs.test.js`).
