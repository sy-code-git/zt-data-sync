@echo off
setlocal
rem ============================================================
rem  编译服务端产物（Windows amd64）
rem  产物输出到 编译\服务端产物\密码本服务端.exe
rem ============================================================
set "ROOT=%~dp0.."
cd /d "%ROOT%"

if not exist "编译\服务端产物" mkdir "编译\服务端产物"

echo 编译 密码本服务端（windows amd64）...
go build -trimpath -ldflags "-s -w" -o "编译\服务端产物\密码本服务端.exe" ./cmd/server
if errorlevel 1 (
    echo [错误] 服务端编译失败
    exit /b 1
)

echo.
echo 服务端编译完成，产物在 编译\服务端产物\
endlocal
