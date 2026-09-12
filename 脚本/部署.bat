@echo off
chcp 65001 >nul

title 在线密码本 - 本地测试一键部署

cd /d "%~dp0\.."



echo ============================================

echo   在线密码本 本地测试环境一键部署（Windows）

echo ============================================

echo.



rem 0. 清理旧服务端进程（支持重复运行）

taskkill /f /im server.exe >nul 2>&1

timeout /t 1 /nobreak >nul



rem 1. 首次生成证书与配置（已存在则跳过，避免覆盖已有数据）

if not exist setup.env (

  echo [1/5] 生成 CA 证书 + 注册配置...

  tools\gen-setup.exe .

  if errorlevel 1 goto :err

) else (

  echo [1/5] 检测到已有配置，跳过证书生成

)



rem 2. 读取配置（按 = 拆分 setup.env：KEY=VAL，均 PB_ 前缀，服务端直接读取）

for /f "usebackq tokens=1* delims==" %%i in ("setup.env") do set "%%i=%%j"

set "PORT=%PB_ADDR:~1%"

echo [2/5] 配置就绪，服务端口 %PORT%



rem 3. 启动服务端（start "" /b 后台同窗口，不弹额外窗口；PB_* 环境变量已 set，子进程继承）

if not exist data mkdir data

if not exist logs mkdir logs

echo [3/5] 启动服务端 https://127.0.0.1:%PORT% ...

start "" /b server\server.exe > logs\server.log 2>&1



rem 4. 等待就绪

echo [4/5] 等待服务端就绪...

:wait

timeout /t 2 /nobreak >nul

curl -sk https://127.0.0.1:%PORT%/healthz | findstr /i ok >nul 2>&1

if errorlevel 1 goto wait

echo      服务端就绪 OK



rem 5. 生成测试信息（bootstrap token 直接取自 setup.env，无需从日志提取）

echo [5/5] 生成管理端注册信息...

> "测试信息.txt" echo 在线密码本 本地测试信息

>>"测试信息.txt" echo 服务端地址: https://127.0.0.1:%PORT%

>>"测试信息.txt" echo CA 证书: %~dp0certs\ca.crt

>>"测试信息.txt" echo REG_SECRET: %PB_REG_SECRET%

>>"测试信息.txt" echo bootstrap token(首次部署填): %PB_BOOTSTRAP_CODE%

>>"测试信息.txt" echo 客户端: client\在线密码本.exe

>>"测试信息.txt" echo 测试指引: 测试清单.md

echo      已生成 测试信息.txt

echo.



echo ============================================

echo  部署完成！

echo    服务端地址 : https://127.0.0.1:%PORT%

echo    CA 证书    : %~dp0certs\ca.crt

echo    REG_SECRET : %PB_REG_SECRET%

echo    bootstrap token : %PB_BOOTSTRAP_CODE%

echo    客户端     : 运行 client\在线密码本.exe

echo    测试指引   : 见 测试清单.md

echo ============================================

echo.

pause

exit /b 0



:err

echo 部署失败，请查看上方错误信息

pause

exit /b 1

