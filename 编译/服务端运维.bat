@echo off
chcp 65001 >nul
setlocal
rem Server Ops: view=show current keys, reset=reset server and gen new keys, ca=download CA cert
set "PY=%USERPROFILE%\.workbuddy\binaries\python\versions\3.13.12\python.exe"
"%PY%" "%~dp0server-ops.py" %1
endlocal
