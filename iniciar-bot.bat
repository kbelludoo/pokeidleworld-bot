@echo off
chcp 65001 >nul
title Poke Idle World - Bot Console
cls

echo ============================================================
echo   ⚡ POKE IDLE WORLD — NATIVE GO MULTI-BOT ENGINE ⚡
echo ============================================================
echo.

if exist "bot-poke.exe" (
    bot-poke.exe
) else (
    go run .
)

pause
