@echo off
chcp 65001 >nul
setlocal
rem Server Ops wrapper. Requires env vars (no defaults in script):
rem   PB_OPS_HOST  PB_OPS_USER  PB_OPS_KEY  PB_OPS_DEPLOY_DIR  [PB_OPS_CA_OUT]
rem Usage: 服务端运维.bat view|reset|ca [--show] [--yes]
set "PY=%USERPROFILE%\.workbuddy\binaries\python\versions\3.13.12\python.exe"
"%PY%" "%~dp0server-ops.py" %*
endlocal
