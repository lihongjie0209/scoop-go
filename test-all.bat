@echo off
setlocal enabledelayedexpansion

set SCOOP_BIN=D:\code\scoop-go\scoop-go.exe
set RESULT_LOG=D:\code\scoop-go\install-test-results.log
set BUCKET_DIR=%USERPROFILE%\scoop\buckets\main\bucket

echo Scoop Go Install Test Suite > %RESULT_LOG%
echo Started: %date% %time% >> %RESULT_LOG%
echo ======================================== >> %RESULT_LOG%

set TOTAL=0
set PASSED=0
set FAILED=0

for %%f in ("%BUCKET_DIR%\*.json") do (
    set "MANIFEST=%%~nf"
    set /a TOTAL+=1

    echo.
    echo [%%TOTAL%%] Testing: !MANIFEST!

    %SCOOP_BIN% install !MANIFEST! >nul 2>&1
    if !errorlevel! equ 0 (
        echo PASS: !MANIFEST! >> %RESULT_LOG%
        set /a PASSED+=1
        %SCOOP_BIN% uninstall !MANIFEST! >nul 2>&1
    ) else (
        echo FAIL: !MANIFEST! >> %RESULT_LOG%
        set /a FAILED+=1
        REM Cleanup failed install
        if exist "%USERPROFILE%\scoop\apps\!MANIFEST!" (
            rmdir /s /q "%USERPROFILE%\scoop\apps\!MANIFEST!" 2>nul
        )
    )
)

echo ======================================== >> %RESULT_LOG%
echo Results: Total=!TOTAL! Passed=!PASSED! Failed=!FAILED! >> %RESULT_LOG%
echo Done: %date% %time% >> %RESULT_LOG%
echo.
echo Results: Total=%TOTAL% Passed=%PASSED% Failed=%FAILED%
echo Log: %RESULT_LOG%
