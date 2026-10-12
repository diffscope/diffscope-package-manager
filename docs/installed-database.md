# Installed Package Database

This document specifies the SQLite index for the local Spec 2.4 package manager. The database schema version is `241`. The [reference DDL](sql/installed-packages.sql) defines all tables and constraints. The [CLI interoperability reference](interoperability.md) specifies the corresponding JSON representations.

## Store Location

The database path is `<packages-root>/.dspm/packages.db`. Package root selection uses `--packages-dir`, then a nonempty `DSPM_PACKAGES_DIR`, then the platform default.

| Platform | Default package root |
| --- | --- |
| Windows | The root is `%LOCALAPPDATA%\OpenVPI\DiffScope_packages`. The user configuration directory provides the base if `LOCALAPPDATA` is absent. |
| macOS | The root is `~/Library/Application Support/OpenVPI/DiffScope_packages`. |
| Other platforms | The root is `$XDG_DATA_HOME/OpenVPI/DiffScope_packages`. The base defaults to `~/.local/share` if `XDG_DATA_HOME` is absent. |

Only packages registered in the database are managed. Unregistered package directories are not imported by queries or installation. An unregistered directory that conflicts with a derived installation path causes an error.

## Schema Identity and Initialization

An existing database must have `PRAGMA user_version = 241`. Automatic migration is not implemented. An unrecognized existing database, including schema version `0`, is rejected. A database at `<packages-root>/packages.db` is also rejected.

```sql
PRAGMA user_version;
```

A missing database represents an empty store for queries if recovery does not require a database commit decision. Ordinary queries do not create a database. Installation initializes a missing index using a transaction containing the DDL and schema version assignment. A missing database with a published transaction journal is not an empty store because the commit state cannot be determined.

The database contains six ordinary SQLite rowid tables. It has no `STRICT` tables, `WITHOUT ROWID` tables, triggers, default column values, or application-defined secondary indexes. SQLite creates indexes for primary keys and the directory uniqueness constraint. Two `CHECK` constraints enforce the opaque contribution boundary.

`PRAGMA foreign_keys = ON` is required on each connection. dspm configures `PRAGMA busy_timeout = 0` and acquires a separate store lock. Foreign key enforcement is a connection setting rather than a persistent database property. The [reference SQL](sql/installed-packages.sql) includes these settings and must run only against a new empty database.

## Column Conventions

All `TEXT` columns use case-sensitive `BINARY` collation. `packages.id` declares the collation explicitly. Other columns use the SQLite default. All columns are `NOT NULL` except `package_texts.name`, `package_texts.description`, and `package_texts.vendor`.

Package versions are normalized strings with four arbitrary-precision decimal integer components. Numeric version comparison must occur outside SQL text ordering. Singer levels use decimal text because SQLite integers cannot represent arbitrary-precision values.

## Tables

### `packages`

Each row identifies an installed package version. Multiple versions of the same package ID can coexist.

| Column | SQL type | Constraint and interpretation |
| --- | --- | --- |
| `id` | `TEXT COLLATE BINARY` | The value is the case-sensitive package ID. |
| `version` | `TEXT` | The value is the normalized installed version. |
| `compat_version` | `TEXT` | The value is the normalized minimum compatible target version. Metadata extraction defaults this value to the package version if absent. |
| `hash` | `TEXT` | The value is the 128-character lowercase SHA-512 content hash recorded during installation. |
| `installed_at` | `INTEGER` | The value is the installation Unix timestamp in seconds. Replacement assigns a new timestamp. An identical-content skip retains the timestamp. |
| `directory` | `TEXT` | The value is the unique 64-character lowercase identity digest naming an immediate child of the package root. |

The primary key is `(id, version)`. `directory` has a `UNIQUE` constraint. Identity syntax, version normalization, `compat_version <= version`, hash syntax, and directory derivation are application constraints rather than SQL `CHECK` constraints. Direct SQL insertion does not establish a valid installation.

### `dependencies`

Each row records a declared dependency target of an installed package.

| Column | SQL type | Interpretation |
| --- | --- | --- |
| `package_id`, `package_version` | `TEXT` | The values identify the owning installed package. |
| `id` | `TEXT` | The value is the required dependency package ID. |
| `version` | `TEXT` | The value is the normalized dependency target version. |

The primary key is `(package_id, package_version, id)`. The foreign key `(package_id, package_version)` references `packages(id, version)` with `ON DELETE CASCADE`.

The target `(id, version)` has no foreign key to a provider. For target version `t`, an installed provider is compatible if `compat_version <= t <= version`. Resolution selects the highest compatible installed version using numeric component comparison. The resolved provider identity is not stored. The final dependency graph must have compatible providers and be acyclic. Only fixed target versions are supported.

### `contributions`

Each row records a contribution identity from `desc.json`. Singer rows can also contain extracted declaration fields. Every category other than the exact ID `singer` is opaque, including `inference` and `singer.extra`.

| Column | SQL type | Interpretation |
| --- | --- | --- |
| `package_id`, `package_version` | `TEXT` | The values identify the owning installed package. |
| `category` | `TEXT` | The value is the extensible dotted category ID. |
| `id` | `TEXT` | The value is the contribution ID within its category. |
| `interface` | `TEXT` | The value is an extracted singer interface ID or `''` if unavailable. Opaque rows require `''`. |
| `level` | `TEXT` | The value is an extracted positive singer level in decimal text or `''` if unavailable. Opaque rows require `''`. |
| `variant` | `TEXT` | The value is an extracted singer variant ID or `''` if unavailable. Opaque rows require `''`. |

The primary key is `(package_id, package_version, category, id)`. The foreign key `(package_id, package_version)` references `packages(id, version)` with `ON DELETE CASCADE`. Different categories within a package can use the same contribution ID.

The following SQL constraint prevents declaration fields from entering opaque rows.

```sql
CHECK (category = 'singer' OR (interface = '' AND level = '' AND variant = ''))
```

Category and ID identify contributions in installed package summaries and contribution selectors for all categories. Singer summaries can additionally include interface, level, variant, and translated name. Opaque declaration files and category-owned entry fields are not parsed for indexing. Opaque contributions have no synthesized name or declaration expansion state.

The empty string sentinel for unavailable singer fields is omitted from JSON summaries. A stored singer level is serialized as a JSON string in summaries and as a JSON number in full metadata. SQL text ordering and integer casts do not preserve arbitrary-precision level ordering.

The singer declaration format requires `interface`, `level`, and `variant`. Indexing records these fields together after successful extraction. If required field extraction fails, all three columns contain `''`. Extractable singer names remain indexed. Index sentinels represent unavailable metadata rather than optional source fields. Full information responses report extraction failures through `metadataError`.

### `package_texts`

Each row records the package text values associated with a language key. Rows use the union of language keys in package name, description, and vendor.

| Column | SQL type | Interpretation |
| --- | --- | --- |
| `package_id`, `package_version` | `TEXT` | The values identify the owning installed package. |
| `language` | `TEXT` | The value is the preserved language key. `_` identifies the default value. |
| `name` | nullable `TEXT` | The value is the package name for this language or `NULL` if absent. |
| `description` | nullable `TEXT` | The value is the package description for this language or `NULL` if absent. |
| `vendor` | nullable `TEXT` | The value is the package vendor for this language or `NULL` if absent. |

The primary key is `(package_id, package_version, language)`. The foreign key `(package_id, package_version)` references `packages(id, version)` with `ON DELETE CASCADE`.

`NULL` indicates absence of a field value for that language. `''` is an explicit empty translation and must remain distinct from `NULL`. For example, a language present only in `name` has `NULL` for `description` and `vendor`. Language keys are preserved without a SQL language-tag validation constraint.

### `contribution_names`

Each row records an extracted singer name for a language key. Opaque contributions have no rows in this table.

| Column | SQL type | Interpretation |
| --- | --- | --- |
| `package_id`, `package_version` | `TEXT` | The values identify the owning installed package. |
| `category`, `contribution_id` | `TEXT` | The values identify the owning contribution. `category` must equal `singer`. |
| `language` | `TEXT` | The value is the preserved language key, including `_`. |
| `name` | `TEXT` | The value is the singer contribution name. An explicit empty string is retained. |

The primary key is `(package_id, package_version, category, contribution_id, language)`. The foreign key `(package_id, package_version, category, contribution_id)` references `contributions(package_id, package_version, category, id)` with `ON DELETE CASCADE`. The SQL constraint is `CHECK (category = 'singer')`.

Successful singer expansion supplies the contribution ID under `_` if no name is declared. Invalid name extraction or failed singer expansion can produce no name rows. This fallback does not apply to opaque contributions.

### `transaction_commits`

| Column | SQL type | Interpretation |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY NOT NULL` | The value is a filesystem transaction ID containing 32 lowercase hexadecimal characters representing 16 random bytes. |

Each row is a recovery commit marker. The row is committed in the same SQLite transaction as package index changes. Successful transaction cleanup removes the row. The table is not installation history and does not independently identify an installed package.

Recovery uses the marker and `.dspm/transactions/<id>/journal.json` to determine whether filesystem changes require rollback or committed cleanup. A marker can remain after committed cleanup is interrupted. Recovery can remove an orphaned marker after confirming that transaction cleanup is complete. Integrations must not delete markers or journal evidence independently.

## Filesystem Identity

The installed directory name is derived from the canonical package selector.

```text
canonical = package_id + "=" + normalized_version
directory = lowercase_hex(SHA256(UTF8(canonical)))
installed_path = packages_root / directory
```

The encoded selector contains no newline, byte order mark, trailing separator, or NUL. Identity comparison is case-sensitive. The database stores the directory basename rather than an absolute path. Locks, the database, and transaction state occupy `.dspm`.

## Content Hash

The content hash is independent of ZIP compression, timestamps, entry ordering, and source directory name. The digest is computed from package file paths and contents rather than archive bytes.

1. Enumerate all regular files, including metadata and unreferenced resources. Represent paths relative to the package root using `/`, exact case, and original Unicode spelling.
2. Sort paths by their UTF-8 byte sequences.
3. Compute the raw 64-byte SHA-512 digest of each file's contents.
4. Concatenate `UTF8(path)`, one zero byte (`0x00`), and the raw file digest for each path in sorted order.
5. Compute SHA-512 over the concatenation and encode the digest as 128 lowercase hexadecimal characters.

Empty directories, file attributes, permissions, and timestamps are excluded. Complete directory preservation requires a separate directory inventory check. Opaque declarations participate in hashing as file bytes without metadata interpretation.

The example function accepts a mapping from package-relative paths to complete file bytes.

```python
import hashlib

def content_hash(files):
    """Returns the lowercase SHA-512 content tree digest, including an empty mapping."""
    tree = hashlib.sha512()
    for path in sorted(files, key=lambda value: value.encode("utf-8")):
        tree.update(path.encode("utf-8"))
        tree.update(b"\x00")
        tree.update(hashlib.sha512(files[path]).digest())
    return tree.hexdigest()
```

## Metadata Ownership

The database is an installation-time management and listing index. `desc.json` retains complete package metadata and opaque entries. Singer declarations retain complete singer metadata. The database excludes declaration bodies, declaration paths, expansion state, extraction error messages, license paths, readme paths, URL, singer media fields, and import or export relationships. It also excludes resolved dependency edges, source archive paths, remote repository records, and installed file inventories.

Installed package contents are immutable under the management protocol. Replacement uses installation with `--overwrite-existing` if the same identity has different content. External file edits can produce stale indexed metadata, dependencies, and recorded hashes. `list` does not rescan declarations. `info --installed` reopens package and singer metadata without reindexing or recomputing the installed hash. A registered identity mismatch is rejected. Opaque declaration files are not parsed by information queries.

## Store Locking

The persistent lock file is `<packages-root>/.dspm/lock`. Cooperating readers acquire a shared lock. Writers and recovery acquire an exclusive lock. The lock file must not be deleted or replaced because replacement permits locks on distinct file objects.

| Platform | Required lock semantics |
| --- | --- |
| Windows | `LockFileEx` locks byte offset `0` with length `1`. Shared reads omit `LOCKFILE_EXCLUSIVE_LOCK`. Exclusive writes include that flag. dspm uses `LOCKFILE_FAIL_IMMEDIATELY`. Release uses `UnlockFileEx` for the same range. |
| Linux and macOS | `flock` uses `LOCK_SH` for readers and `LOCK_EX` for writers. dspm includes `LOCK_NB` and releases with `LOCK_UN`. |

These operations match the `gofrs/flock` dependency. Other platforms require the matching dependency implementation. A SQLite lock or a different operating system lock API does not substitute for the store lock. Store lock contention produces an immediate operational error rather than an internal retry.

## Consistent Reads

Direct index and package file readers must follow this sequence.

1. Acquire the shared store lock on the persistent lock file.
2. Open the existing database in read-only mode, verify schema version `241`, and begin a SQLite read transaction.
3. Require no transaction subdirectories and no `transaction_commits` rows. An absent transactions directory is equivalent to an empty directory. If recovery is pending, close the read transaction and connection, release the lock, and invoke a dspm store query. Restart the sequence after recovery. Pending cleanup or missing recovery evidence requires deferred direct access or use of the CLI result.
4. Read all required tables and access the associated package files while retaining the store lock.
5. Close the read transaction and database connection before releasing the store lock.

A SQLite snapshot without the store lock can represent a different filesystem state during a package directory rename. Independent CLI invocations are separate snapshots and do not constitute a combined read transaction.

Queries and dry runs can perform recovery. Read access that detects pending recovery releases its shared lock and attempts exclusive acquisition. The database is reopened under exclusive access, and recovery runs before the query continues. Failure to acquire the exclusive lock produces an error. Exclusive access remains held through that command.

## Transactions and Recovery

A filesystem transaction stages new contents, publishes a journal, renames package directories, and commits index changes together with a commit marker. The journal is stored at `.dspm/transactions/<id>/journal.json`. Backups and staged contents remain in the transaction directory until cleanup.

Recovery applies the following rules under exclusive access.

| Recovery state | Required operation |
| --- | --- |
| A transaction directory has no published journal. | Recovery removes interrupted staging. |
| A valid journal has no commit marker. | Recovery rolls back the recorded filesystem changes. |
| A valid journal has a commit marker. | Recovery completes committed cleanup. |
| The journal or required package evidence is invalid or unavailable. | Recovery returns an error and retains the unresolved evidence. |

An uncertain database commit is checked using a fresh database connection. If the commit state cannot be determined, transaction evidence remains for subsequent recovery. Committed cleanup failures produce diagnostics and can coexist with successful command completion. Subsequent store access retries cleanup.

The protocol coordinates cooperating processes and process interruption. It does not provide atomic visibility to runtimes that ignore the store lock or guarantee full atomicity across power loss. Package mutations require the complete filesystem and database protocol. Direct SQL inserts or deletions do not implement that protocol.

## Schema Migration

[migrate-240-to-241.sql](sql/migrate-240-to-241.sql) performs the explicit migration from schema version `240` to `241`. No public migration command or automatic version conversion is implemented. Spec 2.3 databases are unsupported.

The migration requires completed recovery, an exclusive store lock, and a verified SQLite backup. Recovery requiring schema version `240` must complete with an executable supporting that schema before migration. The lock must remain held throughout backup, migration, and verification.

The script requires `foreign_keys = ON`, begins an immediate transaction, and rejects any source version other than `240`. The script retains packages, dependencies, package translations, contribution identities, and commit markers. Singer declaration fields and translations are unchanged. Opaque contribution names are removed, and opaque interface, level, and variant fields become empty strings. The two opaque-category `CHECK` constraints are added. Package directories and contents are unchanged.

The caller must stop at the first SQL error and roll back. A script execution API that continues after errors is unsuitable. After successful execution, verification requires schema version `241`, `PRAGMA integrity_check` returning `ok`, no `PRAGMA foreign_key_check` rows, unchanged retained records, and unchanged package contents. The backup must remain available until verification completes. Subsequent operations require an executable supporting schema version `241`.

## Read Queries

The following queries require the locking and schema checks specified above. Parameter binding is required for package identity values. SQL version ordering does not replace numeric comparison in the caller.

```sql
-- Returns installed package fields without numeric version ordering.
SELECT id, version, compat_version, hash, installed_at, directory
FROM packages
ORDER BY id COLLATE BINARY;

-- Returns declared dependency targets for the selected package.
SELECT id, version
FROM dependencies
WHERE package_id = ? AND package_version = ?;

-- Returns contribution identities and available singer translations.
-- The left join retains opaque contributions and singers without names.
SELECT c.category, c.id, c.interface, c.level, c.variant,
       n.language, n.name
FROM contributions AS c
LEFT JOIN contribution_names AS n
  ON n.package_id = c.package_id
 AND n.package_version = c.package_version
 AND n.category = c.category
 AND n.contribution_id = c.id
WHERE c.package_id = ? AND c.package_version = ?
ORDER BY c.category, c.id, n.language;

-- Returns package translations with NULL and empty strings preserved.
SELECT language, name, description, vendor
FROM package_texts
WHERE package_id = ? AND package_version = ?;
```
