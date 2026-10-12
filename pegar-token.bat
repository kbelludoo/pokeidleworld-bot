@echo off
chcp 65001 >nul
title Poke Idle World - Capturador de Token
cls

echo ============================================================
echo   ⚡ POKE IDLE WORLD — CAPTURADOR AUTOMÁTICO DE TOKEN ⚡
echo ============================================================
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\pegar-token.ps1"

pause
