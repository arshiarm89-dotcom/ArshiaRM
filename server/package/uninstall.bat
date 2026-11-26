@echo off
setlocal
title Remove Museum Server - Created By ArshiaRM
net session >nul 2>&1
if %errorlevel% neq 0 (
  echo   Please right-click uninstall.bat and choose "Run as administrator".
  pause
  exit /b 1
)
powershell -NoProfile -Command "Stop-ScheduledTask -TaskName 'MuseumServer' -ErrorAction SilentlyContinue; Unregister-ScheduledTask -TaskName 'MuseumServer' -Confirm:$false -ErrorAction SilentlyContinue" >nul 2>&1
taskkill /im MuseumServer.exe /f >nul 2>&1
netsh advfirewall firewall delete rule name="Museum Server" >nul 2>&1
timeout /t 1 /nobreak >nul
rmdir /s /q C:\MuseumServer >nul 2>&1
echo   The museum server was removed.
pause
