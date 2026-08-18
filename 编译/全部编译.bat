@echo off
setlocal
rem ============================================================
rem  统一编译：先客户端，后服务端
rem ============================================================
set "DIR=%~dp0"

call "%DIR%编译客户端.bat"
if errorlevel 1 exit /b 1

call "%DIR%编译服务端.bat"
if errorlevel 1 exit /b 1

echo.
echo 全部编译完成：编译\客户端产物\ 与 编译\服务端产物\
endlocal
