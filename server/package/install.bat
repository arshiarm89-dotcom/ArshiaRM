@echo off
setlocal
title Museum Server - Created By ArshiaRM
net session >nul 2>&1
if %errorlevel% neq 0 (
  echo.
  echo   Please right-click install.bat and choose "Run as administrator".
  echo.
  pause
  exit /b 1
)
set PORT=8085
set DEST=C:\MuseumServer
echo.
echo   Installing the museum server in %DEST% ...
powershell -NoProfile -Command "Stop-ScheduledTask -TaskName 'MuseumServer' -ErrorAction SilentlyContinue" >nul 2>&1
taskkill /im MuseumServer.exe /f >nul 2>&1
timeout /t 1 /nobreak >nul
if not exist "%DEST%" mkdir "%DEST%"
robocopy "%~dp0." "%DEST%" /E /NFL /NDL /NJH /NJS /NP >nul
if not exist "%DEST%\MuseumServer.exe" (
  echo   Copy failed. Make sure the zip file was extracted first.
  pause
  exit /b 1
)
echo   Opening port %PORT% in Windows Firewall ...
netsh advfirewall firewall delete rule name="Museum Server" >nul 2>&1
netsh advfirewall firewall add rule name="Museum Server" dir=in action=allow protocol=TCP localport=%PORT% >nul
echo   Making it start by itself with Windows ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "Get-ChildItem -Recurse '%DEST%' | Unblock-File -ErrorAction SilentlyContinue; $a = New-ScheduledTaskAction -Execute '%DEST%\MuseumServer.exe' -Argument '-port %PORT%' -WorkingDirectory '%DEST%'; $t = New-ScheduledTaskTrigger -AtStartup; $s = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable; Register-ScheduledTask -TaskName 'MuseumServer' -Action $a -Trigger $t -Settings $s -User 'SYSTEM' -RunLevel Highest -Force | Out-Null; Start-ScheduledTask -TaskName 'MuseumServer'"
timeout /t 3 /nobreak >nul
echo.
echo   Done. The museum server is running and will start by itself after every restart.
echo   A page will open now. It should say the server is on (green check).
echo.
start "" "http://localhost:%PORT%/server"
pause
