@echo off
setlocal
rem ============================================================
rem  编译客户端产物（GUI 客户端 + 钥匙工具 + 密码本命令行）
rem  产物输出到 编译\客户端产物\
rem ============================================================
set "ROOT=%~dp0.."
cd /d "%ROOT%"

if not exist "编译\客户端产物" mkdir "编译\客户端产物"

echo [1/3] 编译 GUI 客户端（wails build）...
cd /d "%ROOT%\client\app"
call wails build
if errorlevel 1 (
    echo [错误] GUI 客户端编译失败
    exit /b 1
)

cd /d "%ROOT%"
echo [2/3] 编译 钥匙工具（keytool）...
go build -trimpath -ldflags "-s -w" -o "编译\客户端产物\钥匙工具.exe" ./cmd/keytool
if errorlevel 1 (
    echo [错误] keytool 编译失败
    exit /b 1
)

echo [3/3] 编译 密码本命令行（pbcli）...
go build -trimpath -ldflags "-s -w" -o "编译\客户端产物\密码本命令行.exe" ./cmd/pbcli
if errorlevel 1 (
    echo [错误] pbcli 编译失败
    exit /b 1
)

rem 复制 wails 产物并重命名为中文
copy /y "client\app\build\bin\app.exe" "编译\客户端产物\在线密码本.exe" >nul

echo.
echo 客户端编译完成，产物在 编译\客户端产物\
endlocal
