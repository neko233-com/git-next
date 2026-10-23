# Runtime licenses and corresponding sources

The complete native product uses its source-built GPL-2.0-only Git engine. Its matching source release contains `native-core/overlay/`, the ordered `native-core/patches/series.json`, compiler/assembly scripts and pinned dependency locks. The original upstream source archive is included in the source bundles. The separate Rust preview keeps its own license and implementation status.

`packaging/corresponding-sources.lock.json` pins 459 original source archives and producer recipes by HTTPS URL, byte count and SHA-256. The four companion source bundles contain MSYS2 source packages, exact Debian source packages, exact installed Homebrew formula sources, and the core/producer build recipes. Producer source packages retain their upstream tarballs and patches. Packaging leaves those archives opaque: it never extracts or runs a `.github/workflows/` file.

Build the source bundles with:

```sh
go -C tools/devtool run . prepare-corresponding-sources --cache /path/to/source-cache --output /path/to/new-source-output
```

The tool reuses only matching cache files, fetches missing files concurrently, checks every size/hash and produces a `sources.json` bundle manifest. Release checksums also cover these source assets. Include all four bundles beside the single matching product release; they are not separate feature releases.

Runtime notices accompany their components. Apple frameworks and OS services remain system requirements. Microsoft redistributable runtime files retain Microsoft's redistribution terms; the source bundles do not represent them as open source. Homebrew producer receipts remove private home-directory paths while preserving original-input hashes in provenance. No account profiles, passwords, private keys or credential-store contents are source inputs.

Source availability, package construction, functional acceptance and performance acceptance are distinct records. A source bundle does not authorize installation or certify compatibility; the fixed [delivery ledger](DELIVERY_STATUS.md) remains authoritative.
