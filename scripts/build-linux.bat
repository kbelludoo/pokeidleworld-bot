@echo off
echo ============================================================
echo   COMPILANDO POKE IDLE WORLD BOT PARA LINUX (AMD64 / VPS)
echo ============================================================

set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o bot-poke-linux .

if %ERRORLEVEL% EQU 0 (
    echo.
    echo ✔ Sucesso! Binario gerado: bot-poke-linux
    echo Envie para a VPS via SCP:
    echo   scp bot-poke-linux ubuntu@IP_DA_VPS:/home/ubuntu/pokeidleworld/
) else (
    echo.
    echo ✖ Erro durante a compilacao.
)
pause
