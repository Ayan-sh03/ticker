$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $unformatted = @(gofmt -l cmd internal)
    if ($LASTEXITCODE -ne 0) { throw 'Go formatting check failed.' }
    if ($unformatted.Count -gt 0) {
        throw "Run gofmt on: $($unformatted -join ', ')"
    }

    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go static analysis failed.' }

    go build ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }

    docker compose --profile tools --profile observability config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Compose validation failed.' }

    Push-Location web
    try {
        npm.cmd run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed. Run npm ci in web first.' }
    }
    finally {
        Pop-Location
    }

    Write-Host 'Formatting, static analysis, builds, and Compose validation passed. No tests were run.'
}
finally {
    Pop-Location
}
