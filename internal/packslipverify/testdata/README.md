# Test fixtures

All files are real, unmodified data; tests that need variants (tampered,
key-signed, other root, ...) mutate them in memory.

## hk-v2.12.0-packslip.sigstore.json

A Packslip release bundle (Sigstore bundle v0.3, DSSE payload type
`application/vnd.in-toto+json`) signed keylessly by the GitHub Actions
workflow `jdx/hk/.github/workflows/release.yml@refs/tags/v2.12.0`.

- Source: https://github.com/jdx/hk/releases/download/v2.12.0/packslip.sigstore.json
- Downloaded: 2026-11-10
- SHA-256: 73551e4ee267f43e6f619dc2b84d99cfa1c2b308d9b28aecb3714eb4de806cc7

## trusted-root-public-good.json

The Sigstore public-good instance trusted root.

- Source: dbc commit `33beb737`, path `internal/packslipverify/testdata/trusted-root-public-good.json`
  (`git show 33beb737:internal/packslipverify/testdata/trusted-root-public-good.json`).
- Byte-identical to `examples/trusted-root-public-good.json` of
  github.com/sigstore/sigstore-go v1.3.0.
- SHA-256: 4364d7724c04cc912ce2a6c45ed2610e8d8d1c4dc857fb500292738d4d9c8d2c
