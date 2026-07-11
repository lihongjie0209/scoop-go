# Local integration test - runs on a subset first, then can run full set
param(
    [int]$MaxTests = 50,
    [string]$ScoopBin = "D:\code\scoop-go\scoop-go.exe",
    [string]$LogFile = "D:\code\scoop-go\install-test-results.log"
)

$bucketDir = "$env:USERPROFILE\scoop\buckets\main\bucket"
$manifests = Get-ChildItem "$bucketDir\*.json" | Sort-Object Name
$total = $manifests.Count
Write-Host "Found $total manifests in main bucket" -ForegroundColor Cyan

# If MaxTests > 0, take a diverse sample (first N, last N, and some random)
if ($MaxTests -gt 0 -and $MaxTests -lt $total) {
    # Pick first 15, last 10, and 25 random from middle
    $sample = @()
    $sample += $manifests | Select-Object -First 15
    $sample += $manifests | Select-Object -Last 10
    $middle = $manifests[15..($total-11)]
    $sample += $middle | Get-Random -Count 25
    $manifests = $sample | Sort-Object { $_.Name }
    Write-Host "Testing $($manifests.Count) packages (diverse sample)" -ForegroundColor Yellow
} else {
    Write-Host "Testing ALL $total packages (this will take a very long time!)" -ForegroundColor Red
}

"========================================" | Out-File $LogFile
"Test started: $(Get-Date)" | Out-File $LogFile -Append
"" | Out-File $LogFile -Append

$passed = 0
$failed = 0
$failures = @()
$count = 0

foreach ($mf in $manifests) {
    $app = $mf.BaseName
    $count++
    Write-Host "[$count/$($manifests.Count)] $app" -NoNewline

    # Parse manifest first to catch JSON issues
    try {
        $manifest = Get-Content $mf.FullName -Raw | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Write-Host " - PARSE FAIL" -ForegroundColor Red
        $failed++
        $failures += "$app (parse error: $_ )"
        "PARSE-ERROR: $app - $_" | Out-File $LogFile -Append
        continue
    }

    # Install
    $output = & $ScoopBin install $app 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host " - PASS" -ForegroundColor Green
        $passed++
        "PASS: $app" | Out-File $LogFile -Append

        # Quick sanity check - does the app have a shim?
        $shims = Get-ChildItem "$env:USERPROFILE\scoop\shims" -Filter "$app.*" -ErrorAction SilentlyContinue
        if ($shims) {
            "  shims: $($shims.Count)" | Out-File $LogFile -Append
        }

        # Uninstall
        & $ScoopBin uninstall $app 2>&1 | Out-Null
    } else {
        Write-Host " - FAIL" -ForegroundColor Red
        $failed++
        $errLine = ($output | Where-Object { $_ -match "Error:|ERROR" }) -join "; "
        $failures += "$app ($errLine)"
        "FAIL: $app - $errLine" | Out-File $LogFile -Append

        # Cleanup
        Remove-Item -Recurse -Force "$env:USERPROFILE\scoop\apps\$app" -ErrorAction SilentlyContinue
        Remove-Item -Recurse -Force "$env:USERPROFILE\scoop\cache\$app*" -ErrorAction SilentlyContinue
    }
}

"" | Out-File $LogFile -Append
"========================================" | Out-File $LogFile -Append
"Results: Total=$count Passed=$passed Failed=$failed" | Out-File $LogFile -Append
"Test ended: $(Get-Date)" | Out-File $LogFile -Append

Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "Results: Total=$count Passed=$passed Failed=$failed" -ForegroundColor $(if ($failed -eq 0) {"Green"} else {"Red"})
if ($failures.Count -gt 0) {
    Write-Host "`nFailures:" -ForegroundColor Red
    $failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
}
Write-Host "`nLog: $LogFile"
