# dspm CLI Interoperability

This document specifies the implemented local CLI output for DiffScope package Spec 2.4. Package format versions, executable versions, and database schema versions are independent identifiers. The database schema version is `241`. CLI responses have no protocol version field.

The following references define the associated formats and storage constraints.

- The [CLI output JSON Schema](schemas/cli-output.schema.json) specifies individual output values using JSON Schema Draft 2020-12.
- The [installed database reference](installed-database.md) specifies index fields, store locking, recovery, and consistent reads.
- The [SQLite reference DDL](sql/installed-packages.sql) defines an empty database with schema version `241`.
- The [schema migration SQL](sql/migrate-240-to-241.sql) defines the explicit migration from schema version `240` to `241`.

## Command Selection

`--json` enables structured output. `DSPM_JSON` supplies the default value. An explicit `--json=false` disables structured output. Accepted true environment values are `1`, `t`, `T`, `TRUE`, `true`, and `True`. All other environment values produce a false default.

`--packages-dir` overrides `DSPM_PACKAGES_DIR` and the platform default package root. `--language` affects text output only. JSON output retains all extracted language keys. `--columns` and its alias `--column` affect text tables only, but invalid column arguments still produce errors in JSON mode.

```text
dspm --json list
dspm --json info --file ./voice.dspk
dspm --json info --installed example/voice=1.0
dspm --json info --installed example/voice=1.0:singer/main
dspm --json install --file ./voice.dspk ./base.dspk
dspm --json remove example/voice=1.0
dspm --json pack ./voice --output ./voice.dspk
```

| Command | Output format | Successful payload |
| --- | --- | --- |
| `info --file <archive>` | The command returns one JSON response. | `data` contains an `Info` object with `file` and `hash`. |
| `info --installed <selector>` | The command returns one JSON response. | `data` contains an `Info` object with `installation`. |
| `list` | The command returns one JSON response. | `data` contains an array of `InstalledPackage` objects. |
| `install --file <archive>...` | The command returns NDJSON records. | The stream contains installation events. |
| `remove <selector>...` | The command returns NDJSON records. | The stream contains removal events. |
| `pack <directory>` | The command returns NDJSON records. | The stream contains packing events. |
| Root `--version` | The command returns one JSON object without an envelope. | The object contains `name` and the executable `version`. |

`ls` is an alias for `list`. `rm` and `uninstall` are aliases for `remove`. The `command` field contains the canonical command name for these aliases.

`info --file` and `info --installed` are mutually exclusive. `install` and `info` select an unimplemented remote operation if their local source flag is absent. That operation terminates with `panic("not implemented")` and emits no structured response. Help output and root invocation without `--version` remain text output with `--json` enabled.

Integrations must place `--json` before subcommands and arguments. Errors that prevent JSON flag parsing can produce text output. Argument errors can identify the selected command or the root command `dspm`.

## Transport

Structured output uses UTF-8 without a byte order mark. Each JSON value terminates with LF (`\n`). Each NDJSON record occupies one physical line. Embedded newlines in strings are escaped. NDJSON has no surrounding array or separators between records other than LF. Object member order, insignificant whitespace, and equivalent string escape sequences have no semantic significance.

Structured responses and progress records use stdout. Diagnostics appear in JSON fields. stderr is not a JSON channel and can contain panic or runtime diagnostics. Integrations must capture stdout, stderr, and process exit status separately. JSON mode emits no terminal control sequences.

Output conforms to strict JSON. Support for comments and a byte order mark in package declarations does not apply to CLI output.

## Completion

A successful data query requires exit status `0` and a response with `ok: true`. A successful mutation requires exit status `0` and a terminal `summary`. Root `--version` requires exit status `0` and a valid version response. Reported command failures exit with status `1`. A panic or forced termination can produce another nonzero status without a terminal record. Output failures can produce truncated JSON or an incomplete stream.

`plan`, `inspect_ok`, `staged`, `result`, and `progress` records do not independently establish command completion. A successful `summary` with `dryRun: true` describes a plan rather than an applied mutation. A terminal record does not establish success if the process subsequently exits with a nonzero status.

An unsuccessful or interrupted invocation does not establish that no changes occurred. A database commit can precede an output failure or process termination. After an uncertain outcome, a new store query performs applicable recovery and returns the current registered state. Recovery has no public command.

## Schema Validation

The [JSON Schema](schemas/cli-output.schema.json) validates one query response, mutation record, or version response. NDJSON consumers must validate each record separately. Definitions under `$defs` provide reusable payload schemas.

Schema validation does not verify stream ordering, completion, counter relationships, dependency resolution, content hashes, or consistency between package files and index records. These constraints require the checks specified in this document and the database reference.

Object schemas permit additional properties for additive extensions. Integrations must ignore unrecognized properties unless an applicable schema constraint rejects them. Opaque contribution schemas explicitly reject derived declaration fields. The event enumeration covers implemented events. Validation of additional event types requires an updated schema. Unknown error codes require generic failure handling.

## Query Envelopes

A successful query contains `ok`, `command`, `data`, and `diagnostics`. `ok` is `true`. `command` is `info` or `list`. `data` has the command-specific type. An empty list result is `[]`.

```json
{"ok":true,"command":"list","data":[],"diagnostics":null}
```

A reported query failure contains `ok`, `command`, `error`, and `diagnostics`. `ok` is `false`. `data` is absent.

```json
{"ok":false,"command":"info","error":{"code":"NOT_FOUND","message":"The requested package is not installed."},"diagnostics":null}
```

Envelope `diagnostics` is an array of strings or `null`. `null` and `[]` both indicate absence of diagnostics. Extracted optional fields are omitted if unavailable. Arbitrary JSON fields retained in declarations can contain `null`.

The root version response contains no query envelope. The executable version is a build identifier and need not contain four numeric components.

```json
{"name":"dspm","version":"0.0.0+dev"}
```

## Shared Types

| Type | Representation |
| --- | --- |
| `Identity` | The object contains a package `id` and normalized `version`. |
| Package ID | The string contains ASCII segments separated by `/`. Each segment contains at least one letter, digit, underscore, or hyphen. Comparison is case-sensitive. |
| Contribution ID | The string contains one package ID segment without `/`. |
| Category, interface, or variant ID | The string contains contribution ID segments separated by `.`. Category IDs are extensible and case-sensitive. |
| Package version | The string contains four decimal integer components, such as `"1.2.0.0"`. Components have arbitrary precision and no leading zeros except the value `0`. |
| `Text` | The object maps language keys to strings and includes the default key `_`. Extracted shorthand strings become `{"_":"value"}`. Empty strings and all language keys are retained. |
| `FilePaths` | The object has the `Text` representation. Every value is a nonempty, fully qualified, lexically normalized host path to a regular file inside the package. |
| `Dependency` | The object contains a package `id` and normalized target `version`. The target does not identify a resolved provider version. |
| Content hash | The string contains 128 lowercase hexadecimal characters representing the [content tree digest](installed-database.md#content-hash). |

Package selectors use `id=version`. Contribution selectors use `id=version:category/contribution-id`. A selector can omit `=version` if exactly one version of that package is installed. Multiple installed versions produce `AMBIGUOUS_VERSION` if the version is omitted. Removal accepts package selectors only.

Version comparison uses numeric comparison of the four components. Lexicographic string comparison and conversion to fixed-width integers do not preserve version semantics. Arbitrary declaration numbers and full contribution `level` values can exceed the exact integer range of JavaScript numbers. Integrations requiring exact values must use an arbitrary-precision JSON number representation.

## Installed Package Payload

`InstalledPackage` is the index representation returned by `list` and `Info.installation`.

| Field | JSON type | Constraint |
| --- | --- | --- |
| `id`, `version` | string | Both fields are required and identify the installed package. |
| `compatVersion` | string | The field is required and contains the normalized minimum compatible target version. |
| `hash` | string | The field is required and contains the content hash recorded during installation. |
| `installedAt` | integer | The field is required and contains a Unix timestamp in seconds. |
| `directory` | string | The field is required and contains a 64-character lowercase identity digest naming an immediate child of the package root. |
| `dependencies` | array | The field is required and contains declared `Dependency` objects. Absence of dependencies is represented by `[]`. |
| `contributions` | array | The field is required and contains `ContributionSummary` objects. Absence of contributions is represented by `[]`. |
| `name`, `description`, `vendor` | `Text` | Each field is optional and contains indexed package translations. |

`list` reads the database without reopening package declarations. Packages are sorted by ID in ascending order and then by numeric version in descending order. Indexed contributions are sorted by category and ID in ascending order. Other collection ordering has no identity or relationship semantics.

## Contribution Summaries

A singer contribution has category ID exactly equal to `singer`. An opaque contribution has any other category ID, including `inference` or `singer.extra`.

`ContributionSummary` contains required `category` and `id` fields. A singer summary can additionally contain `name` (`Text`), `interface` (string), `variant` (string), and `level` (decimal integer string). An opaque summary contains only `category` and `id`. Summaries contain no declaration path, declaration body, or singer resource fields.

## Package Information

`Info` contains required `package` (`PackageManifest`). `info --file` additionally returns `file` and `hash` and omits `installation`. `file` preserves the supplied archive argument. `hash` is computed from all archive file contents and includes integrity verification.

`info --installed` additionally returns `installation` (`InstalledPackage`) and omits top-level `file` and `hash`. The recorded installation hash is `installation.hash`. The command reopens `desc.json` and applicable singer declarations but does not rebuild the index or recompute the installed content hash. A manifest identity that differs from the registered identity produces an error.

### Package Manifest

`PackageManifest` contains required `id`, `version`, `formatVersion`, `runtimeLevel`, `compatVersion`, `dependencies`, `contributions`, and `manifest`. Optional fields are `name`, `description`, `vendor`, `license`, `readme`, `url`, and `diagnostics`. `dependencies` and `contributions` are arrays, including `[]` if empty. `diagnostics`, if present, is a nonempty string array.

| Declaration field | Extracted representation |
| --- | --- |
| `$version` | `formatVersion` contains the normalized format version. The supported value is `"1.0.0.0"`. |
| `runtimeLevel` | `runtimeLevel` contains the supported integer value `1`. |
| `id`, `version`, `compatVersion` | The extracted fields retain their names. Versions are normalized. An absent `compatVersion` defaults to `version`. |
| `name`, `description`, `vendor` | Each extracted field contains a complete `Text` object. |
| `license`, `readme` | Each extracted field contains a complete `FilePaths` object. All language keys are retained. |
| `url` | The extracted field contains the expanded string. An absent field is omitted. An explicit empty string remains `""`. URI syntax and network availability are not checked. |
| `dependencies`, `contributions` | The extracted arrays contain declared dependencies and full contribution objects. |
| `vars` | Variable definitions are consumed during expansion and omitted from the expanded manifest. |

`license` and `readme` use identical path rules. A declaration accepts a shorthand string such as `"license": "LICENSE.txt"` or a language map such as `"license": {"_": "LICENSE.txt", "zh-CN": "docs/许可证.txt"}`. A language map requires the default key `_`. Variable expansion precedes path resolution. Every localized path is checked, regardless of the selected output language. Null values, empty paths, missing files, directories, and references outside the package are rejected. An absent field remains omitted.

`manifest` contains expanded `desc.json` with arbitrary fields and JSON types retained. It does not preserve source formatting, comments, or byte order marks. Typed normalization does not modify the retained declaration representation. For example, `manifest.version` can contain `"1.0"` while the typed `version` contains `"1.0.0.0"`. `manifest.license` and `manifest.readme` contain expanded declaration paths while typed `license` and `readme` contain resolved host paths.

The database stores package name, description, and vendor for installed package summaries. License paths, readme paths, URL, and arbitrary extension fields remain available through full information responses and are absent from installed package summaries.

### Contributions

| Field | Constraint |
| --- | --- |
| `category`, `id` | Both fields are required. The category identifies the containing `desc.json` contribution array. The ID is extracted from the entry. |
| `entry` | The field is required and contains the expanded `desc.json` entry with arbitrary fields retained. |
| `path` | The field is optional and contains a checked package-relative file path using `/` separators. |
| `manifest` | The field is required for singer and absent for opaque contributions. It contains the referenced singer declaration or inline singer entry. |
| `expanded` | The field is required for singer and absent for opaque contributions. It indicates successful singer declaration expansion. |
| `name` | The field is optional and available only for singer. An absent singer name defaults to `{"_": id}` after successful expansion. An invalid singer name remains unavailable. |
| `interface`, `variant` | Each field is optional and available only for singer as an extracted dotted ID. |
| `level` | The field is optional and available only for singer as a positive JSON integer with arbitrary precision. |
| `singer` | The field is available after successful singer expansion. It contains optional `avatar`, `background`, and `demoAudio` (`Text`) and can be `{}`. |

A string `path` directly in a `desc.json` contribution entry must reference a regular file inside the package. A non-string `path` in an opaque entry remains uninterpreted entry data and produces no derived `path` field. A non-string singer entry path is rejected.

Opaque declaration files are not parsed or expanded. Binary data, malformed JSON, duplicate keys, and invalid declaration variables in these files do not affect metadata loading. Opaque entry fields such as `name`, `interface`, `level`, and `variant` remain under `entry` without declaration field extraction, category-specific validation, indexing, or fallback values. Package variable expansion applies to the complete `desc.json`, including opaque entries. Category and ID remain indexed and available in contribution selectors. All resource bytes remain subject to archive integrity checks, content hashing, and extraction limits.

Singer declarations are parsed for metadata extraction. Invalid or unreadable declaration JSON is rejected. Failed variable expansion retains the raw declaration in `manifest`, sets `expanded` to `false`, omits derived fields, and records diagnostics. Individual field extraction failures omit the affected field and produce diagnostics. Singer resource paths, resource contents, imports, exports, and interpreter configuration are not validated.

Full singer `level` uses a JSON number. Indexed summaries and the database use decimal text. Full `level: 2`, summary `level: "2"`, and SQL `level = '2'` represent the same integer.

### Selectors and Path Contexts

An installed contribution selector filters only `package.contributions`. `package.manifest` and `installation.contributions` retain the complete package declaration and index.

`${root}`, `${dir}`, and typed license and readme paths depend on the read context. Installed information uses the installed package directory. Pack planning uses the source directory. Archive information uses the archive parent directory as a logical root without extracting files there. A singer declaration has `${dir}` set to its containing directory. Extracted singer resource strings are not independently resolved or normalized. Expanded absolute paths are specific to the applicable read context.

## Mutation Envelopes

An ordinary mutation record contains required `command`, `event`, and `data`. An error record contains required `command`, `event`, `error`, and `diagnostics` and omits `data`. Mutation records contain no `ok` field. `summary` is the terminal successful record. No additional success envelope follows it.

### Progress

Mutations emit `progress` records on stdout in JSON mode, including redirected output. Queries return a single response without progress records.

```json
{"command":"install","event":"progress","data":{"phase":"extract","target":"example/voice=1.0.0.0","state":"update","filePath":"resources/model.bin","current":131072,"total":1048576,"filesDone":1,"filesTotal":3}}
```

| Field | Constraint |
| --- | --- |
| `phase` | The value is `inspect`, `resolve`, `extract`, `verify`, `commit`, `check`, or `pack`. |
| `target` | The value identifies the archive argument for inspection, package selector for extraction or verification or packing, package root for resolution or commit, or source directory for checking. |
| `state` | The value is `start`, `update`, or `done`. `done` indicates successful completion of that phase. |
| `filePath` | The value is the current package-relative file path using `/`. The value is `""` if no file is selected. |
| `current`, `total` | The values are processed and expected uncompressed file bytes for the phase. Directories and ZIP overhead are excluded. |
| `filesDone`, `filesTotal` | The values are successfully processed and expected regular file counts. Empty files are included. |

All progress fields are required. Counters are nonnegative integers. A `start` record initializes counters to zero. A subsequent update can supply known totals. Zero `total` indicates an unknown total or absence of byte processing. Percentage calculation requires a positive total.

Counters apply to the current phase and target rather than the complete batch. Inspection, extraction, verification, and packing use streaming byte counts with intermediate updates for large files. Resolution, directory checking, and commit use zero counters.

Start records, done records, file path changes, completed file counts, and final byte counts are retained. Other updates are coalesced with a nominal interval of 100 milliseconds. File boundaries can produce a higher record rate. The record count and update interval are not fixed.

| Command | Phase sequence |
| --- | --- |
| Installation | Each input has `inspect`. The batch then has `resolve`, each changed package has `extract` and `verify`, and the batch has `commit`. |
| Removal | The batch has `resolve` and `commit`. |
| Packing | The operation has `check`, `pack`, and `verify`. Archive publication precedes the verification `done` record. |

Dry runs omit extraction, archive writing, verification, and commit. Identical-content installation skips omit extraction and verification. A normal installation with only skip actions can still emit a commit phase without a database mutation.

Progress can occur between application events. Reaching byte totals does not establish validation, publication, or commit success. A failed phase omits `done`. Cancellation during an active operation can produce `CANCELLED`. A completed commit remains authoritative if cancellation occurs afterward.

### Installation Events

| Event | Payload |
| --- | --- |
| `inspect_start` | `data` contains the nonempty array of supplied archive arguments. |
| `inspect_ok` | `data` contains an `Identity` after metadata and archive content validation. |
| `plan` | `data` contains all installation actions. |
| `staged` | `data` contains an `Identity` after extraction and staged content verification. |
| `result` | `data` contains an installation action. |
| `summary` | `data` contains a `MutationSummary`. |

Excluding progress records, successful ordering is `inspect_start`, one `inspect_ok` per input, `plan`, zero or more `staged` records, one `result` per action, and `summary`. Results follow successful batch commit processing. `skip` actions have no staged package. Dry runs emit the plan, results, and summary without staging or commit.

### Removal Events

Excluding progress records, successful ordering is optional `ignored`, `plan`, one `result` per action, and `summary`. `plan` contains removal actions. `result` contains a removal action. `summary` contains `MutationSummary`.

`--ignore-non-existent` permits missing package targets. A nonempty `ignored` record contains the supplied missing selectors and precedes `plan`. The summary repeats the array as `ignored`. All ignored targets can produce an empty action array and no result records.

`--cascade` includes dependent packages that would otherwise become invalid. Direct selections use action `remove`. Additional dependent selections use action `cascade`. Dry runs report the planned actions without applying them.

The following NDJSON example contains removal application events. Progress records are excluded.

```ndjson
{"command":"remove","event":"plan","data":[{"package":{"id":"example/voice","version":"1.0.0.0"},"action":"remove"}]}
{"command":"remove","event":"result","data":{"package":{"id":"example/voice","version":"1.0.0.0"},"action":"remove"}}
{"command":"remove","event":"summary","data":{"actions":[{"package":{"id":"example/voice","version":"1.0.0.0"},"action":"remove"}],"dryRun":false}}
```

### Actions and Mutation Summaries

`Action` contains required `package` (`Identity`) and `action`. Installation actions use `install`, `overwrite`, or `skip` and include required `file` and `hash`. `file` preserves the archive argument. `hash` contains the inspected content hash. Removal actions use `remove` or `cascade` and omit `file` and `hash`.

`MutationSummary` contains required `actions` and `dryRun`. `actions` is the complete action array. `dryRun` is a boolean for both installation and removal. Optional `diagnostics` and removal `ignored` are nonempty string arrays.

`skip` indicates an installed identity with identical recorded content. Different content with the same identity requires `--overwrite-existing` and produces `overwrite`. Different versions can coexist. Dependency validation applies to the final registered package set, including all batch actions.

Successful committed operations can include cleanup diagnostics. Pending cleanup is retried during subsequent store access. Diagnostics do not invalidate an otherwise successful committed result.

### Packing Events

| Event | Payload |
| --- | --- |
| `plan` | `data` contains `PackPlan` with required `source`, `output`, `files`, `directories`, `bytes`, and `package`. |
| `result` | `data` contains required `file`, `hash`, `package` (`Identity`), and `size`. |
| `summary` | `data` contains required nullable `diagnostics` and optional `dryRun`. |

`PackPlan.source` and `PackPlan.output` are absolute filesystem paths. `files` and `directories` are package-relative path arrays using `/`, sorted by UTF-8 byte order. Empty path collections can be `null`. `bytes` is the total uncompressed regular file size. `package` contains a complete `PackageManifest`. An output archive within the source directory is excluded from package contents.

Packing `result.file` is an absolute path. `result.hash` is the content hash. `result.size` is the archive size in bytes, including ZIP overhead.

Excluding progress records, normal ordering is `plan`, `result`, and `summary`. A dry run emits `plan` and `summary` and omits `result`. Packing summaries contain `dryRun: true` for dry runs and omit `dryRun` for normal completion. Packing summary diagnostics can be `null` or a string array.

```json
{"command":"pack","event":"summary","data":{"diagnostics":null}}
```

## Errors and Diagnostics

A reported mutation error is terminal and has no subsequent successful summary. It can occur after partial output or as the first record.

```json
{"command":"install","event":"error","error":{"code":"INVALID_PACKAGE","message":"The package ID is invalid."},"diagnostics":null}
```

`error` contains required `code` and `message` and optional `details`. `message` and diagnostic strings have no machine-stable substructure. Integrations must classify errors by `code` rather than message parsing. `details` has no common shape except the documented ambiguity payload.

Example messages illustrate error conditions and do not specify exact diagnostic text.

| Code | Condition |
| --- | --- |
| `INVALID_ARGUMENT` | Command arguments, flags, selectors, or filters are invalid. |
| `INVALID_PACKAGE` | Package metadata, archive validation, or staged content validation failed. |
| `CONFLICT` | A batch identity is duplicated, installed content differs without replacement permission, or packing output exists without replacement permission. |
| `DEPENDENCY_ERROR` | Provider selection or dependency graph validation failed. |
| `NOT_FOUND` | The requested installed package or contribution is absent. |
| `AMBIGUOUS_VERSION` | The selector omits a version and multiple versions are installed. `details` contains an array of canonical package selectors. |
| `CANCELLED` | The reported error contains context cancellation. |
| `IO_ERROR` | An operational error has no more specific classification. Filesystem, database, lock, and recovery failures can use this code. |

Errors during package inspection can be classified as `INVALID_PACKAGE` even if the underlying failure is operational. Conflicts detected during store commit can be classified as `IO_ERROR`.

Package extraction diagnostics can appear in both query envelope `diagnostics` and `data.package.diagnostics`. Packing plan diagnostics can recur in the summary. Repeated strings in these locations do not establish separate failures. Envelope diagnostics and packing summary diagnostics can be `null`. Optional package diagnostics and mutation summary diagnostics are omitted if empty.

## Database Integration

Only registered packages are managed. The SQLite index is `<packages-root>/.dspm/packages.db`. The index contains management fields and installed package summaries. Complete package metadata and singer declarations remain in installed files. Opaque declaration contents are excluded from metadata extraction and indexing.

CLI queries provide coordinated index and filesystem access. Direct readers must follow the [store locking and recovery requirements](installed-database.md#consistent-reads). A SQLite transaction alone does not coordinate package directory renames or perform recovery. Direct SQL writes do not implement package installation or removal.

The [reference DDL](sql/installed-packages.sql) creates an empty database. It neither imports package directories nor upgrades an existing database. The separate migration script requires a verified backup and exclusive store access.

## Implementation References

[output.go](../internal/cli/output/output.go) defines response rendering. [progress.go](../internal/cli/output/progress.go) defines progress coalescing. [commands.go](../internal/cli/commands/commands.go) and [root.go](../internal/cli/root.go) define command selection and argument error handling. [internal/packagemanager](../internal/packagemanager) defines payloads and event emission. [manifest.go](../packageinfo/manifest.go) and [singer.go](../packageinfo/singer.go) define metadata extraction. [database.go](../packagedatabase/database.go) defines index storage.
