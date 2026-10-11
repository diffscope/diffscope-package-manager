# DiffScope Package Manager

`dspm` manages DiffScope resource packages using Spec 2.4 metadata. This stage implements local package management. Packages contain resources; the manager does not load interpreters or execute package content.

## Build and test

Go 1.26.1 or newer and a C compiler are required for SQLite. On Windows, use MinGW:

```powershell
$env:CGO_ENABLED = '1'
$env:CC = (Get-Command gcc).Source
go build -o dspm.exe ./cmd/dspm
go test ./...
go test -race ./...
go vet ./...
```

Linux and macOS builds also require CGO and a platform C compiler. CI runs tests, race checks and builds on all three platforms.

## Commands

```text
dspm pack ./voice --output ./voice.dspk
dspm install --file ./voice.dspk ./base.dspk
dspm install --file ./voice.dspk --overwrite-existing
dspm info --file ./voice.dspk
dspm info --installed vendor/voice=1.0
dspm info --installed vendor/voice=1.0:singer/main
dspm list
dspm remove vendor/voice=1.0
dspm remove base=1.0 --cascade
```

`install` and `info` select the unimplemented remote source if their local source flag is absent. These branches terminate with `panic("not implemented")`, including in JSON mode. `--file` selects local archives. `info --installed` selects registered package metadata. Source selection is explicit and does not depend on filenames.

Package selectors use `package=version`; contribution selectors use `package=version:category/id`. Omitting the version is allowed only when one version of the package is installed. Identity matching is case-sensitive. Different versions coexist. Dependencies select the highest installed version satisfying `compatVersion <= target <= version`, without backtracking. All dependencies must be installed already or supplied in the same batch.

Installation skips identical contents. Replacing different contents with the same identity requires `--overwrite-existing`, intended for local development. Removing a dependency is allowed when another compatible version still satisfies its dependents; otherwise use `--cascade` to remove dependents that become invalid. Packages outside the local database are ignored. Old databases are rejected without migration.

Global options include `--packages-dir`, `--json` and `--language`. `DSPM_PACKAGES_DIR` and `DSPM_JSON` provide environment defaults. Text language selection preserves explicit empty values; JSON retains every language key. Queries emit one JSON object; mutations emit newline-delimited JSON events. Diagnostics use stderr in text mode and structured fields in JSON mode. `list --columns` accepts `id,version,name,vendor,description,installed-at,hash,directory,compat-version`.

See the [interoperability documentation](docs/interoperability.md) for JSON/NDJSON payloads, the output JSON Schema, and the installed SQLite database reference and SQL DDL.

Interactive text output uses inspection spinners and progress bars with a package or file title above a colored block bar and a percentage on the right. Batch installation displays a title and progress bar for each package requiring installation. JSON mutation events include byte and phase progress for verification and commit. Queries return a single JSON object. Ctrl+C cancels the operation and removes uncommitted staging. `ls` aliases `list`, `--column` aliases `--columns`, and `--language '*'` displays all translations in text mode.

`install`, `remove` and `pack` support `--dry-run`. This previews the requested operation without applying it. Opening an existing store can first recover an earlier interrupted transaction, including during a query or dry-run; that maintenance is reported. Empty queries and dry-runs do not initialize a database. There is no separate recovery command.

`pack` accepts `desc.json` and preserves original file bytes. TOML/YAML conversion and media checks have been removed. Existing output requires `--force`; output is validated before publication. ZIPs reject links, special files, unsafe or colliding paths, excessive resources and CRC errors. Empty directories are preserved. Defaults are 100,000 entries including implicit parent directories, 64 GiB per file, 256 GiB total, 4096 UTF-8 bytes per path, and a compression ratio of 1000. The archive library exposes configurable limits.

## Metadata and architecture

Only the `singer` category has its declaration file parsed. All other categories, including `inference`, retain their expanded `desc.json` entry and expose category, ID and any checked direct file path. Their declaration files are not parsed, their variables are not expanded, and declaration fields are not extracted or indexed. Their category and ID remain indexed and usable in contribution selectors. No name or declaration expansion state is fabricated for them. Singer contributions additionally expose common module fields, avatar, background and demo audio when extractable. Contribution-specific fields and import/export relationships are not validated. String `path` values directly in `desc.json` and package license and readme paths must reference regular files inside the package. Resource paths within contribution declarations are neither checked nor opened. A non-string `path` in an opaque category is retained as category-owned data.

The package manifest and singer JSON declarations support UTF-8 BOM and comments, reject duplicate keys and trailing commas, and preserve unknown fields. Variable expansion follows declaration order and scope and never rescans substituted text. Parsing limits include 16 MiB per parsed declaration, 128 levels, one million value nodes, and a 64 MiB cumulative metadata budget. Malformed singer fields produce extraction diagnostics rather than installation failures; unreadable or invalid singer JSON declarations are rejected. These JSON requirements do not apply to opaque contribution declaration files. All package files still participate in archive integrity checks, content hashing and extraction limits.

If singer declaration variable expansion fails, its raw declaration is retained with `expanded: false`, derived fields are unavailable, and the failure appears in diagnostics. Valid and inline singers have `expanded: true`. Opaque contributions expose their expanded `desc.json` entry only through `entry`; they have no declaration `manifest`, `expanded` state or derived declaration metadata.

- `packageinfo`: shared metadata loading, identifiers, versions, JSON and variables.
- `packagearchive`: bounded archive/directory access, hashing, extraction and packing.
- `packagedatabase`: minimal SQLite indexes, explicit schema and private ORM models.
- `internal/dependency`: dependency selection and graph validation without I/O.
- `internal/localstore`: store sessions, persistent process locks, staging, journals, commit and recovery.
- `internal/packagemanager`: local use cases and results, independent of CLI presentation.
- `internal/cli`: command/source selection and text/JSON rendering.

Installed package directories use identity digests; `.dspm` holds the index, lock and transaction records. Reads coordinate with writers. Mutations retain old directories until the index and a commit marker commit together. Startup recovery restores uncommitted changes or completes committed cleanup. These guarantees cover cooperating dspm processes and process interruption; they do not provide cross-package atomic visibility to runtimes that ignore the lock or guarantee atomicity across power loss.

A read that triggers recovery retains the exclusive lock until that command finishes. Missing recovery evidence or an indeterminate commit state stops the operation and preserves the remaining evidence. Committed cleanup failures are warnings and are retried on the next access.

Future remote discovery and archive acquisition can use the same neutral dependency candidates, metadata loader and installation transaction. CPR publishing/validation and remote operations are outside this stage.
