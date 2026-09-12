@echo off
chcp 65001 >nul

title 在线密码本 - 测试环境重置

rem 自定位工作目录：本目录存在 tools\gen-setup.exe 即测试包根；否则视为仓库 脚本\ 子目录，上溯仓库根
if exist "%~dp0tools\gen-setup.exe" (cd /d "%~dp0") else (cd /d "%~dp0\..")



echo ============================================

echo   在线密码本 测试环境重置

echo ============================================

echo.

echo 将清理以下内容，恢复到首次部署状态：

echo   [服务端] setup.env / certs / data / logs / 测试信息.txt

echo   [客户端] client 目录下的本地数据库（*.db / *.db-shm / *.db-wal）

echo.

set /p ANS=确认重置？[y/N] 

if /i not "%ANS%"=="y" (

  echo 已取消重置

  pause

  exit /b 0

)



echo.

echo [1/3] 停止服务端与客户端进程...

taskkill /f /im server.exe >nul 2>&1

taskkill /f /im 在线密码本*.exe >nul 2>&1

timeout /t 1 /nobreak >nul



echo [2/3] 清理服务端运行产物...

if exist setup.env del /f /q setup.env

if exist certs rmdir /s /q certs

if exist data rmdir /s /q data

if exist logs rmdir /s /q logs

if exist 测试信息.txt del /f /q 测试信息.txt



echo [3/3] 清理客户端本地数据...

del /f /q client\*.db 2>nul

del /f /q client\*.db-shm 2>nul

del /f /q client\*.db-wal 2>nul



echo.

echo 重置完成！环境已恢复到首次部署状态。

echo 重新双击「部署.bat」即可重新初始化。

echo.

pause

exit /b 0

