# Remote operations

## Source selection

`install <selector>...` and `info <selector>` use OVCPR v1 repositories. `--registry` specifies the package base URL, including the `packages/` path.

```sh
dspm --registry https://example.org/packages/ info vendor/voice
dspm --registry https://example.org/packages/ install vendor/voice=1
dspm install --file ./voice.dspk ./base.dspk
dspm install --file-only ./voice.dspk ./base.dspk
```

A versioned remote selector selects the exact normalized version. An unversioned selector selects the highest numeric version in the first source containing the package. Dependency acquisition selects the highest compatible provider in the first matching source. Network failures and invalid metadata are errors rather than package absence. Deprecated versions remain eligible for resolution and produce diagnostics.

Explicit archives take precedence over installed dependencies and remote sources. All explicit archives are installation roots. Adjacent files are not scanned. The final installed graph selects its highest compatible providers and undergoes complete validation.

## Configuration

Configuration precedence is flags, environment variables, the user configuration file, and defaults. `--registry` is repeatable and replaces the complete source list in argument order. `--config` specifies the configuration file. The default path is `os.UserConfigDir()/OpenVPI/dspm/config.json`. Configuration loading does not create the file.

```json
{
  "configurationVersion": 1,
  "registries": [{"name": "team", "url": "https://example.org/packages/"}],
  "cacheDir": "/path/to/dspm-cache",
  "downloadConcurrency": 4,
  "maxDownloadBytes": 68719476736,
  "allowedDownloadHosts": ["downloads.example.org:443"],
  "sourceRules": [{"prefix": "team/", "sources": ["team"]}]
}
```

`DSPM_REGISTRIES` accepts a JSON array of source URLs. `DSPM_REGISTRY` is the single-source alternative. The two variables cannot coexist unless a command-line source list replaces them. `DSPM_CACHE_DIR` selects the cache root. `--cache-dir` overrides that selection. `--download-concurrency` overrides archive concurrency. The default concurrency is four, and accepted values range from one to sixteen.

Source rules match exact package IDs or slash-terminated prefixes. The longest match applies. Rule source names restrict authority selection and replica discovery. Duplicate normalized source URLs and duplicate source names are rejected. The default source is `https://ovcpr.diffscope.org/packages/`. Any configured source list replaces that default.

| Platform | Default cache root |
| --- | --- |
| Windows | The root is `%LOCALAPPDATA%/OpenVPI/dspm/Cache`. |
| macOS | The root is `~/Library/Caches/OpenVPI/dspm`. |
| Linux | The root is `$XDG_CACHE_HOME/OpenVPI/dspm`. The base defaults to `~/.cache` if `XDG_CACHE_HOME` is absent. |

## Metadata integrity

Every selection verifies raw-byte SHA-512 links from the root index to the versions index and from the versions index to the manifest. Selection retains its source revision and expected digests throughout preparation. Inconsistent CDN generations trigger at most three complete planning attempts. Requests, retries, elapsed time, and cumulative metadata bytes are bounded.

Archive acquisition starts after complete dependency planning. A replica requires the same exact identity, archive digest, and immutable package semantics. Replica download URLs and safe documentation mappings may differ. Replica discovery occurs only after an archive source fails. Failover cannot change selected versions or dependencies.

## Network policy

Default archive downloads require HTTPS, certificate verification, and public destination addresses. Explicit registry endpoints may use private addresses. Archive targets require public addresses or an exact user-configured host and port allowlist. Direct requests bind validated DNS addresses to the connection. Environment proxies are supported. Proxy requests report `addressValidation: proxy-delegated` because the proxy resolves the destination.

`--allow-unrestricted-downloads` permits HTTP and all destination addresses for the current invocation, including redirects. HTTPS certificate verification, content verification, resource budgets, and source rules remain effective. Metadata and persistent configuration cannot enable this flag.

Presentation URLs omit credentials, query strings, and fragments. Internal request URLs and manifest bytes remain available for exact verification and network access.

## Download recovery

Archives stream into a digest-addressed cache. Per-object process locks prevent duplicate transfers across cooperating processes. Synchronized checkpoints record resumable prefixes. HTTP `206` responses require valid ranges. An ignored Range request restarts from the same `200` response. HTTP `416` establishes completion only if the complete file digest matches. Requests to a different resource do not inherit the previous resource's ETag.

Retries, requests, idle time, total time, and bytes have finite budgets. Download completion establishes the archive file digest. ZIP integrity, CRCs, package format, runtime level, metadata projection, and content-tree verification precede staging. Successful acquisition alone does not establish installation.

Disk space prechecks use the known remaining archive bytes and staged package bytes on their respective volumes. Write errors remain authoritative because available space can change during an operation. Prechecks do not require a fixed reserve.

Network activity occurs outside the installation store lock. Installation reacquires the lock and checks the installed snapshot before staging. Concurrent changes trigger bounded replanning. Completed verified archives remain reusable during replanning.

## Dry runs and progress

Remote installation `--dry-run` returns `verification: metadata-only`, an empty action array, planned candidates, and pending archive checks. It does not download archives or initialize an empty database. Local dry runs fully validate explicit archives. Queries and dry runs may recover an interrupted transaction in an existing store.

`--progress auto` selects interactive rendering if the destination supports terminal control. Each active download displays its identity, status, received and total byte counts, speed, available ETA, and cache status. Known totals use progress bars and percentages. Unknown totals use animated indicators without percentages. Completed tasks appear in the summary. Terminal dimensions limit visible task rows, and a separate count records hidden tasks. Download and extraction progress use separate byte totals.

`--progress plain` uses plain text without ANSI styling. Redirected output also uses plain text. `--progress none` suppresses progress rendering. JSON retains task progress in every mode.

The [CLI interoperability reference](interoperability.md) specifies progress fields, terminal states, source records, and structured diagnostics.

## Cache management

```sh
dspm cache dir
dspm cache info
dspm cache clean --downloads --dry-run
dspm cache clean --metadata
dspm cache clean --partial
```

`cache dir` returns the configured path without initializing the cache. `cache info` returns managed file counts, byte counts, and activity. `cache clean` removes managed data. The mutually exclusive `--downloads`, `--metadata`, and `--partial` flags select a cleanup scope. With no scope flag, cleanup includes all managed data. `--dry-run` previews any scope without deleting data.

Cleanup preserves stable locks and the identity marker. Active leases prevent cleanup. Failure to acquire the cleanup lock within ten seconds returns `CACHE_BUSY`. Unmarked nonempty directories, linked cache roots, internal links, and special files are rejected. Existing parent aliases are resolved before boundary checks. Automatic cache eviction is not provided.

Responses containing `Cache-Control: no-store` use temporary files. These files cannot provide resume state across invocations.

## Submission generation

```sh
dspm repo create --file ./release.dspk --repository ./community-repository --download-url https://example.org/releases/1/release.dspk
```

`repo create` requires an existing repository directory. It validates the exact archive, computes its file-byte SHA-512, derives a portable projection under two distinct virtual roots, and copies raw license and readme bytes. The download URL is not contacted. The result therefore contains `remoteVerified: false`.

Published identities and assets cannot be overwritten. An identical repeated invocation preserves existing formatting, safe custom asset mappings, and deprecation state. The private `.dspm-repo` directory contains the journal and lock. Assets are published before the manifest without replacement. A subsequent invocation completes known interrupted publication. Unknown user files are retained. Generator state must remain untracked.

`repo create --dry-run` validates and previews publication without initializing generator state. The command does not generate indexes, commits, or pull requests. OVCPR validation independently downloads each new archive and checks metadata and documentation asset bytes.

## Shared APIs

`packagearchive.NewReaderAt` borrows the supplied handle. Closing the archive does not close that handle. The caller must keep the handle open and its bytes stable during archive access. `HashFileBytes` computes the digest of exactly the specified file byte range. This digest differs from the installed content-tree digest.

`packagerepository` provides strict OVCPR v1 decoding, portable publication projection, metadata comparison, and asset mapping. `packagedependency` provides provider selection and graph validation. These libraries do not access configuration, networks, or installation databases. OVCPR imports the public libraries at a fixed module version.

Ordinary installation uses documentation inside the archive. Installation does not download or verify separately hosted repository documentation assets.

## Information Output

Remote text information uses the Package, Dependencies, Singers, and Download sections. Shared package fields and singer fields use the same labels and order as archive and installed information. Documentation fields contain resolved asset URLs in the Package section. Empty dependency and singer collections are explicit. Singer summaries appear directly under Singers because remote manifests contain only singer contributions. Repository provenance and query policy fields are available in JSON output. Diagnostics remain available in both output formats. Archive declaration bodies, media references, opaque contributions, and installation records require local information queries.
