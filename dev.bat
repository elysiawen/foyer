@echo off
setlocal
cd /d "%~dp0"

echo [dev] killing old instances...
taskkill /f /im foyer.exe >nul 2>nul 2>&1
taskkill /f /im air.exe >nul 2>nul 2>&1

if /i "%~1"=="reset" (
    del /q local.db local.db-shm local.db-wal >nul 2>nul
    echo [dev] local database reset
)

if not exist tmp mkdir tmp

rem ---- env ----
set "FOYER_ADDR=127.0.0.1:9100"
set "FOYER_DB=%~dp0local.db"
set "FOYER_BASE_DOMAIN=abc.com"
set "FOYER_COOKIE_DOMAIN=localhost"
set "FOYER_SECURE_COOKIE=false"
set "FOYER_ADMIN_USER=admin"
set "FOYER_ADMIN_PASSWORD=foyer12345"

echo.
echo   URL:  http://localhost:9100
echo   User: admin / foyer12345
echo   DB:   %FOYER_DB%
echo.
echo   air watching for file changes...
echo   Ctrl+C to stop
echo.

air