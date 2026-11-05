param([switch]$DatabaseMatrix,[switch]$DockerBuild)
$ErrorActionPreference = 'Stop'
$invoiceRoot = Split-Path $PSScriptRoot -Parent
Push-Location $invoiceRoot
try {
    Push-Location web
    try {
        bun install --frozen-lockfile
        if ($LASTEXITCODE) { throw 'dependency install failed' }
        foreach ($check in @('typecheck','lint','test','build')) {
            bun run $check
            if ($LASTEXITCODE) { throw "Web $check failed" }
        }
    } finally { Pop-Location }
    $env:GIN_MODE = 'release'
    go test -count=1 ./...
    if ($LASTEXITCODE) { throw 'Go tests failed' }
    go vet ./...
    if ($LASTEXITCODE) { throw 'Go vet failed' }
    go mod verify
    if ($LASTEXITCODE) { throw 'Go dependency verification failed' }
    go build -o output/invoice.exe ./cmd/invoice
    if ($LASTEXITCODE) { throw 'Go build failed' }
    if ($DatabaseMatrix) {
        foreach ($port in @(55417,55418)) {
            # This address and database are dedicated test fixtures; never accept a production DSN here.
            $env:INVOICE_TEST_DATABASE_URL = "postgres://postgres:invoice_test_only@127.0.0.1:$port/invoice_test?sslmode=disable"
            go test -count=1 -timeout=120s ./internal/store ./internal/server
            if ($LASTEXITCODE) { throw "PostgreSQL test matrix failed on $port" }
        }
    }
    if ($DockerBuild) {
        docker build --tag invoice:v0.1-local .
        if ($LASTEXITCODE) { throw 'Docker build failed' }
    }
} finally {
    Remove-Item Env:INVOICE_TEST_DATABASE_URL -ErrorAction SilentlyContinue
    Pop-Location
}
