@echo off
setlocal
cd /d "%~dp0"

echo [1/3] stopping old instance...
taskkill /f /im foyer.exe >nul 2>nul
taskkill /f /im nasnav.exe >nul 2>nul

if /i "%~1"=="reset" (
    del /q local.db local.db-shm local.db-wal >nul 2>nul
    echo local database reset
)

set "FOYER_ADDR=127.0.0.1:9100"
set "FOYER_DB=%~dp0local.db"
set "FOYER_BASE_DOMAIN=abc.com"
set "FOYER_COOKIE_DOMAIN=localhost"
set "FOYER_SECURE_COOKIE=false"
set "FOYER_ADMIN_USER=admin"
set "FOYER_ADMIN_PASSWORD=foyer12345"

echo [2/3] build...
go build -o dist\foyer.exe .\cmd\foyer
if errorlevel 1 (
    echo build failed
    pause
    exit /b 1
)

echo [3/3] start
echo.
echo   URL:  http://localhost:9100
echo   User: admin / foyer12345
echo   DB:   %FOYER_DB%
echo   Ctrl+C to stop
echo.

dist\foyer.exe
