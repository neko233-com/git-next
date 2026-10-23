# Retired Python CLI suites

The 30 CLI conformance scripts in this directory are retired immediately. Direct
execution exits with an error and points to `git-next-accept`. They are source
references, never merge/tag/release gates. Do not extend them or add a Python CLI
runner. The Python aggregate wrapper has been removed.

Use [Rust acceptance](../../docs/GIT_NEXT_ACCEPTANCE.md) for L0/full/network.
Retirement does not claim that every historical assertion has been migrated.
The current scope and remaining coverage are recorded there; old passing reports
remain historical evidence for their exact bytes.

`push_acceptance.HttpFixture` and `lfs_transfer_acceptance.Fixture` remain importable
by the separate native package/credential/migration and benchmark workflows. Only
their fixture services are reused; their retired CLI `main` functions are not run.
Native package, GUI, platform API and installer lifecycle scripts remain outside
this CLI migration. Packaging and documentation generation may still use Python.
