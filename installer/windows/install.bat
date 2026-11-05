@echo off
rem One-click Windows installer for the portable NGG bundle.
rem Double-click this file after extracting NGG-<version>-windows-amd64.zip.
rem It re-launches install.ps1 with administrator privileges (UAC prompt).
setlocal
cd /d "%~dp0"

if not exist "nggd.exe" (
  echo [NGG][ERROR] nggd.exe not found in this folder.
  echo Extract the whole ZIP first, then run install.bat from the extracted folder.
  pause
  exit /b 1
)
if not exist "install.ps1" (
  echo [NGG][ERROR] install.ps1 not found in this folder.
  pause
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -Command "$d=(Get-Location).Path; $p=Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File','install.ps1','-Source','.','-OpenBrowser') -WorkingDirectory $d -Verb RunAs -Wait -PassThru; exit $p.ExitCode"
echo.
if errorlevel 1 (
  echo [NGG] Installation failed. Read the red message in the elevated window.
) else (
  echo [NGG] Done. The Web UI should have opened in your browser.
  echo      Access info: NGG-ACCESS.txt in the install directory.
)
pause
exit /b %errorlevel%
