# Installs Treadle on Windows 10 or later, with nothing else required:
#
#     irm https://github.com/heddlehq/Treadle/releases/latest/download/install.ps1 | iex
#
# Everything goes in %LOCALAPPDATA%\Treadle. To remove it, delete that folder and
# take it out of your user PATH. Run it again to update. Your login is kept
# elsewhere, so updating won't sign you out.
#
# TREADLE_HOME     install somewhere else
# TREADLE_VERSION  install a specific version, such as 0.1.0

function Get-TreadleDownloadUrl {
  $repository = 'https://github.com/heddlehq/Treadle'

  if ($env:TREADLE_DOWNLOAD_URL) {
    return $env:TREADLE_DOWNLOAD_URL
  }
  if ($env:TREADLE_VERSION) {
    return "$repository/releases/download/v$($env:TREADLE_VERSION.TrimStart('v'))"
  }
  return "$repository/releases/latest/download"
}

function Install-Treadle {
  $ErrorActionPreference = 'Stop'
  # The progress bar makes Invoke-WebRequest many times slower.
  $ProgressPreference = 'SilentlyContinue'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

  $downloadUrl = Get-TreadleDownloadUrl
  $archive = 'treadle-windows-x64.tar.gz'
  $treadleHome = if ($env:TREADLE_HOME) { $env:TREADLE_HOME } else { Join-Path $env:LOCALAPPDATA 'Treadle' }
  $bin = Join-Path $treadleHome 'bin'
  $work = Join-Path ([IO.Path]::GetTempPath()) ("treadle-" + [Guid]::NewGuid())

  if (-not [Environment]::Is64BitOperatingSystem) {
    throw 'Treadle needs a 64-bit version of Windows.'
  }

  # Git's tar may come first on PATH, and it reads C:\ as a remote host.
  $tar = Join-Path $env:SystemRoot 'System32\tar.exe'
  if (-not (Test-Path $tar)) {
    throw 'Treadle needs Windows 10 version 1803 or later.'
  }

  New-Item -ItemType Directory -Force -Path $work | Out-Null

  try {
    Write-Host 'Downloading Treadle for Windows...'
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$archive" -OutFile "$work\$archive"
    Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$archive.sha256" -OutFile "$work\$archive.sha256"

    $expected = (Get-Content "$work\$archive.sha256" -Raw).Trim().Split(' ')[0]
    $actual = (Get-FileHash "$work\$archive" -Algorithm SHA256).Hash
    if ($expected -ne $actual) {
      throw 'The download is damaged. Run the installer again.'
    }

    New-Item -ItemType Directory -Force -Path "$work\app" | Out-Null
    & $tar -xzf "$work\$archive" -C "$work\app"
    if ($LASTEXITCODE -ne 0) {
      throw 'Could not unpack Treadle.'
    }

    # Run it before replacing anything, so a bad build can't break a working install.
    $version = & "$work\app\treadle.exe" --version
    if ($LASTEXITCODE -ne 0) {
      throw "This build doesn't run on your system. Nothing was changed."
    }

    New-Item -ItemType Directory -Force -Path $bin | Out-Null
    Move-Item -Force "$work\app\treadle.exe" (Join-Path $bin 'treadle.exe')
  }
  finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
  }

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $bin) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath, $bin) -join ';').Trim(';'), 'User')
  }
  $env:Path = "$env:Path;$bin"

  Write-Host ''
  Write-Host "$version is installed in $treadleHome"
  Write-Host 'Open a new terminal and run `treadle`.'
}

# Inside a function so that `| iex` can't close the person's terminal on an error.
# Last on purpose: a connection cut halfway through runs nothing.
try {
  Install-Treadle
}
catch {
  Write-Host ''
  Write-Host $_.Exception.Message -ForegroundColor Red
  Write-Host ''
  # Only a script run from a file has a caller that reads this.
  if ($PSCommandPath) {
    exit 1
  }
}
