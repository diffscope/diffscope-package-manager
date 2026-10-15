param(
    [Parameter(Mandatory = $true)]
    [string]$ExecutablePath,

    [Parameter(Mandatory = $true)]
    [string]$SymbolsDir,

    [Parameter(Mandatory = $true)]
    [string]$ArchivePath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true

$executable = (Resolve-Path -LiteralPath $ExecutablePath).Path
$symbols = [System.IO.Path]::GetFullPath($SymbolsDir)
$archive = [System.IO.Path]::GetFullPath($ArchivePath)
$executableName = [System.IO.Path]::GetFileName($executable)
if ((Test-Path -LiteralPath $symbols) -or (Test-Path -LiteralPath $archive)) {
    throw 'The symbol directory and archive must not already exist'
}
New-Item -ItemType Directory -Path $symbols | Out-Null
New-Item -ItemType Directory -Force -Path ([System.IO.Path]::GetDirectoryName($archive)) | Out-Null

if ($IsMacOS) {
    $llvmObjcopyCommand = Get-Command llvm-objcopy -ErrorAction SilentlyContinue
    $llvmObjcopy = if ($llvmObjcopyCommand) { $llvmObjcopyCommand.Source } else { $null }
    if (-not $llvmObjcopy) {
        # Hosted macOS images provide a versioned, keg-only Homebrew LLVM package.
        $brew = Get-Command brew -ErrorAction SilentlyContinue
        if ($brew) {
            $llvmFormulas = @(& $brew.Source list --formula | Where-Object { $_ -match '^llvm(?:@\d+)?$' })
            foreach ($formula in $llvmFormulas) {
                $prefix = (& $brew.Source --prefix $formula | Out-String).Trim()
                $candidate = Join-Path $prefix 'bin/llvm-objcopy'
                if (Test-Path -LiteralPath $candidate -PathType Leaf) {
                    $llvmObjcopy = $candidate
                    break
                }
            }
        }
    }
    if (-not $llvmObjcopy) {
        throw 'llvm-objcopy is required to remove embedded Mach-O DWARF sections'
    }

    $dsym = Join-Path $symbols "$executableName.dSYM"
    # Go embeds linked DWARF and removes its temporary object-file debug map.
    # Update mode reads the embedded DWARF instead of requiring those object files.
    & xcrun dsymutil --update --no-odr $executable -o $dsym | Out-Host
    $dwarf = Join-Path $dsym "Contents/Resources/DWARF/$executableName"
    if (-not (Test-Path -LiteralPath $dwarf) -or (Get-Item -LiteralPath $dwarf).Length -eq 0) {
        throw 'The dSYM bundle contains no DWARF file'
    }
    $debugInfo = & xcrun dwarfdump --debug-info $dwarf
    if (-not ($debugInfo -match 'DW_TAG_compile_unit')) {
        throw 'The dSYM bundle contains no compilation units'
    }
    $binaryUUID = (& xcrun dwarfdump --uuid $executable | Out-String)
    $symbolUUID = (& xcrun dwarfdump --uuid $dwarf | Out-String)
    $uuidPattern = 'UUID: ([0-9A-Fa-f-]+)'
    $binaryMatch = [regex]::Match($binaryUUID, $uuidPattern)
    $symbolMatch = [regex]::Match($symbolUUID, $uuidPattern)
    if (-not $binaryMatch.Success -or -not $symbolMatch.Success -or
        $binaryMatch.Groups[1].Value -ne $symbolMatch.Groups[1].Value) {
        throw 'The dSYM UUID differs from the executable UUID'
    }
    # Apple strip -S removes STABS entries from executables, but retains embedded
    # DWARF. LLVM objcopy removes the sections in __DWARF without a second link.
    & $llvmObjcopy --strip-debug $executable | Out-Host
    $loadCommands = & xcrun otool -l $executable
    if ($loadCommands -match '^\s*sectname\s+__(?:zdebug|debug)_') {
        throw 'The executable still contains DWARF sections'
    }
    & codesign --force --sign - $executable | Out-Host
    & codesign --verify $executable | Out-Host
    $strippedUUID = (& xcrun dwarfdump --uuid $executable | Out-String)
    $strippedMatch = [regex]::Match($strippedUUID, $uuidPattern)
    if (-not $strippedMatch.Success -or
        $strippedMatch.Groups[1].Value -ne $symbolMatch.Groups[1].Value) {
        throw 'The stripped executable UUID differs from the dSYM UUID'
    }
} else {
    if ($IsWindows) {
        # Go emits DWARF rather than PDB. Use binutils from the selected MinGW toolchain.
        $compilerDir = Split-Path -Parent $env:CC
        $objcopy = Join-Path $compilerDir 'objcopy.exe'
        $objdump = Join-Path $compilerDir 'objdump.exe'
        if (-not (Test-Path -LiteralPath $objcopy) -or -not (Test-Path -LiteralPath $objdump)) {
            throw 'The selected MinGW toolchain must provide objcopy.exe and objdump.exe'
        }
        # Delve's PE loader reads DWARF from the executable itself. Preserve this exact
        # linked image for Delve, without a second link that could change code addresses.
        $unstripped = Join-Path $symbols 'unstripped'
        New-Item -ItemType Directory -Path $unstripped | Out-Null
        Copy-Item -LiteralPath $executable -Destination (Join-Path $unstripped $executableName)
    } else {
        $objcopy = (Get-Command objcopy -ErrorAction Stop).Source
        $objdump = (Get-Command objdump -ErrorAction Stop).Source
    }
    $debugFile = Join-Path $symbols "$executableName.debug"
    & $objcopy --only-keep-debug $executable $debugFile | Out-Host
    $sections = & $objdump -h $debugFile
    if (-not ($sections -match '\s\.debug_info\s+0*[1-9a-fA-F][0-9a-fA-F]*\s')) {
        throw 'The debug companion contains no .debug_info data'
    }
    # Retain runtime tables, PE base relocations and unwind information.
    # Strip only debugging data, and link the companion by its basename and CRC.
    & $objcopy --strip-debug "--add-gnu-debuglink=$debugFile" $executable | Out-Host
    $sections = & $objdump -h $executable
    if ($sections -match '\s\.(?:zdebug|debug)_\w+\s') {
        throw 'The executable still contains DWARF sections'
    }
}

$archiver = Get-Command 7z, 7zz -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $archiver) {
    throw '7z or 7zz is required to archive debug symbols'
}
Push-Location -LiteralPath $symbols
try {
    & $archiver.Source a -t7z -mx=9 -ms=on $archive . | Out-Host
} finally {
    Pop-Location
}
if (-not (Test-Path -LiteralPath $archive) -or (Get-Item -LiteralPath $archive).Length -eq 0) {
    throw 'The debug symbol archive is missing or empty'
}
