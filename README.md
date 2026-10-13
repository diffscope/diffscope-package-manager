# DiffScope Package Manager

`dspm` manages DiffScope resource packages with Spec 2.4 metadata and OVCPR v1 repositories. Packages contain resources. Installation does not execute package contents or load inference engines.

## Build requirements

The executable requires Go 1.26.1 or later, CGO, and a platform C compiler for SQLite. Windows builds require a compatible MinGW compiler.

```powershell
$env:CGO_ENABLED = '1'
$env:CC = (Get-Command gcc).Source
go build -o dspm.exe ./cmd/dspm
go test ./...
go test -race ./...
go vet ./...
```

Linux and macOS builds require `CGO_ENABLED=1` and a C compiler. CI includes tests, race detection, static analysis, and builds on Windows, Linux, and macOS. The shared `packageinfo`, `packagearchive`, `packagedependency`, and `packagerepository` libraries do not require CGO.

## Release builds

Release builds require an explicit executable version and a reviewed source revision. The version is independent of package format and database schema versions. The following PowerShell command requires `DSPM_RELEASE_VERSION` to contain the release identifier.

```powershell
go build -trimpath -ldflags "-X github.com/diffscope/diffscope-package-manager/internal/cli.version=$env:DSPM_RELEASE_VERSION" -o dspm.exe ./cmd/dspm
go version -m ./dspm.exe
./dspm.exe --json --version
```

Published binaries require recorded platform, architecture, source revision, and file checksums. Release validation includes the tests and static analysis specified above.

## Package operations

```sh
dspm info vendor/voice
dspm info --versions vendor/voice
dspm download vendor/voice=1.0 --output ./archives
dspm --offline install vendor/voice=1.0
dspm install vendor/voice=1.0
dspm install --file ./voice.dspk ./base.dspk
dspm install --file-only ./voice.dspk ./base.dspk
dspm info --file ./voice.dspk
dspm info --installed vendor/voice=1.0
dspm info --installed vendor/voice=1.0:singer/main
dspm list
dspm remove vendor/voice=1.0
dspm remove base=1.0 --cascade
dspm pack ./voice --output ./voice.dspk
```

`download` exports verified archives and their complete dependency collection without reading the installation database. `--offline` requires cached repository metadata and archives and prohibits network requests. `info --versions` lists available versions from the selected source.

`install` and `info` use remote sources unless a local source flag is present. `install --file` reads explicit archives and obtains missing dependencies remotely. `install --file-only` uses explicit archives and installed dependencies without configuration, registry, or download cache access. Adjacent archives are not scanned. `info --file` reads an archive. `info --installed` reads registered metadata.

Package selectors use `id=version`. Contribution selectors use `id=version:category/contribution-id`. Versions contain four nonnegative integer components. An abbreviated version such as `1.2` normalizes to `1.2.0.0`. Identity comparison is case-sensitive. Multiple versions of a package can coexist.

An unversioned remote selector selects the highest numeric version in the first matching source. An unversioned installed selector requires exactly one installed version. Dependency acquisition prefers explicit archives, installed providers, and configured sources, in that order. Resolution selects the highest compatible provider satisfying `compatVersion <= target <= version`, without backtracking. The final installed graph requires compatible providers and no cycles.

Installation skips identities with identical recorded content. Different content under the same identity requires `--overwrite-existing`. Removal requires a compatible remaining provider for every dependent package. `--cascade` includes dependents that would otherwise become invalid. Packages absent from the database are unmanaged.

`install`, `download`, `remove`, and `pack` support `--dry-run`. Remote installation dry runs validate metadata without downloading archives. Local installation dry runs validate explicit archives. Queries and dry runs may recover an interrupted transaction in an existing store. Empty queries and dry runs do not initialize a database.

## Repositories and cache

```sh
dspm --registry https://example.org/packages/ info vendor/voice
dspm --registry https://example.org/packages/ install vendor/voice=1
dspm cache dir
dspm cache info
dspm cache clean --partial --dry-run
dspm repo create --file ./voice.dspk --repository ./community-repository --download-url https://example.org/releases/1/voice.dspk
```

Repository selection verifies the SHA-512 links between root indexes, versions indexes, and manifests. Downloaded archives require the manifest file digest, archive integrity, package metadata, and publication projection to match before staging. Download recovery uses persistent checkpoints and validated HTTP Range responses. Exact-content replicas may provide an archive if the authority source is unavailable.

`cache dir` returns the configured cache path. `cache info` reports managed file counts, byte counts, and activity. `cache clean` removes managed data and preserves lock files and the identity marker. `repo create` generates an immutable manifest and documentation assets from an exact archive without network access. Repository acceptance and deployment require separate OVCPR validation.

The [remote operations reference](docs/remote-operations.md) specifies configuration, network policy, cache lifecycle, download recovery, and submission generation.

## Output and configuration

`--packages-dir` overrides the installation root. `DSPM_PACKAGES_DIR` supplies its environment default. `--json` enables structured output, with `DSPM_JSON` as the environment default. Queries return one JSON object. Mutations return newline-delimited JSON events. Text diagnostics use stderr. JSON diagnostics use structured fields.

`--language` controls text translation selection. Explicit empty translations remain empty. JSON preserves every language key. `--language '*'` displays all translations in text mode. `list --columns` accepts `id,version,name,vendor,description,installed-at,hash,directory,compat-version`. `--column` aliases `--columns`. `ls` aliases `list`. `rm` and `uninstall` alias `remove`.

Interactive output includes phase progress and parallel download progress. Downloads with known totals display progress bars and percentages. Downloads with unknown totals display animated indicators and received byte counts. `--progress plain` disables terminal control sequences. `--progress none` suppresses progress rendering. JSON output retains progress events. Ctrl+C cancels active operations. Store recovery removes uncommitted staging on subsequent access.

The [CLI interoperability reference](docs/interoperability.md) specifies payloads, events, progress, diagnostics, and the [output JSON Schema](docs/schemas/cli-output.schema.json). The [installed database reference](docs/installed-database.md) specifies storage, locking, recovery, and SQL integration.

## Package validation

`pack` accepts directories containing `desc.json` and preserves file bytes and empty directories. Existing output requires `--force`. Archive publication follows validation. Links, special files, unsafe paths, path collisions, CRC failures, and resource limit violations are rejected. Default limits are 100,000 entries including implicit directories, 64 GiB per file, 256 GiB total, 4096 UTF-8 bytes per path, and a compression ratio of 1000. The archive library exposes configurable limits.

Package manifests and singer declarations require UTF-8 JSON without a byte order mark or comments. Duplicate keys and trailing commas are rejected. Unknown declaration fields are preserved. Variable expansion follows declaration order and scope. Substitution results are not rescanned. Default parsing limits are 16 MiB per declaration, 128 nesting levels, one million value nodes, and 64 MiB of cumulative metadata.

Only the exact contribution category `singer` receives declaration parsing. Singer entries require a declaration `path`. Successful common metadata extraction includes `interface`, `level`, and `variant`. Failed required extraction retains the declaration, records `metadataError`, and omits those three fields. Failed variable expansion retains the raw declaration with `expanded: false`. Invalid or unreadable singer JSON rejects the package. Optional extraction failures produce diagnostics. Singer media references, import and export relationships, and interpreter configuration are not validated.

Other categories are opaque. Their expanded `desc.json` entries, category IDs, contribution IDs, and checked direct file paths remain available. Opaque declaration files are not parsed or expanded. A non-string opaque entry `path` remains category-owned data. String paths directly in contribution entries and package license and readme paths must reference regular package files. All package files participate in integrity checks and content hashing.

## Architecture

| Package | Responsibility |
| --- | --- |
| `packageinfo` | Metadata loading includes identifiers, versions, JSON parsing, and variables. |
| `packagearchive` | Archive access, hashing, extraction, and packing enforce resource limits. |
| `packagedependency` | Provider selection and graph validation operate without I/O. |
| `packagerepository` | OVCPR v1 decoding, publication projection, and comparison operate without network or database access. |
| `packagedatabase` | The SQLite index stores installation records and metadata summaries. |
| `internal/localstore` | Store sessions coordinate locks, staging, journals, commit, and recovery. |
| `internal/packagemanager` | Package operations and results are independent of CLI presentation. |
| `internal/cli` | Commands select sources and render text or JSON output. |

Installed directories use identity digests. `.dspm` contains the database, lock, and transaction records. Filesystem changes and index updates use a journal and a database commit marker. Recovery restores uncommitted changes or completes committed cleanup. Missing evidence or an indeterminate commit state rejects the operation and preserves recovery evidence. Cleanup failures produce diagnostics and are retried on subsequent access.

The transaction protocol coordinates cooperating processes and process interruption. It does not provide cross-package atomic visibility to runtimes that ignore the store lock or guarantee atomicity across power loss.
