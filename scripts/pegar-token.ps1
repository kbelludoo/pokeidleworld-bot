param()

Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "   POKE IDLE WORLD - CAPTURADOR AUTOMATICO DE TOKEN" -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host ""

$root = Resolve-Path "$PSScriptRoot\.."
$accountsFile = Join-Path $root "accounts.json"
$keyFile = Join-Path $root "ssh-key-2026-10-06.key"
$vpsHost = "129.153.157.17"

$token = ""
$refreshToken = ""

function Parse-InputTokens($raw) {
    if (-not $raw) { return }
    $raw = $raw.Trim()
    
    # Se for JSON com pokeweb:tokens {"accessToken":"...", "refreshToken":"..."}
    if ($raw.StartsWith("{") -and $raw.Contains("accessToken")) {
        try {
            $parsed = ConvertFrom-Json $raw
            if ($parsed.accessToken) {
                $script:token = $parsed.accessToken
                if ($parsed.refreshToken) {
                    $script:refreshToken = $parsed.refreshToken
                }
                return
            }
        } catch {}
    }

    # Se for JWT puro
    if ($raw -like "ey*.*.*") {
        $script:token = $raw
        return
    }
}

try {
    $clip = [System.Windows.Forms.Clipboard]::GetText()
    if (-not $clip) {
        $clip = (Get-Clipboard -Raw)
    }
    Parse-InputTokens $clip
    if ($token) {
        Write-Host "[!] Token detectado na area de transferencia (Ctrl+C)!" -ForegroundColor Green
        if ($refreshToken) {
            Write-Host "    [+] RefreshToken tambem detectado! (Auto-renovacao ativada)" -ForegroundColor Green
        }
        $len = [Math]::Min(35, $token.Length)
        Write-Host "    $($token.Substring(0, $len))..." -ForegroundColor Gray
        $confirm = Read-Host "Deseja utilizar este token? (S/N) [Padrao: S]"
        if ($confirm -ne '' -and $confirm -ne 'S' -and $confirm -ne 's') {
            $token = ""
            $refreshToken = ""
        }
    }
} catch {}

if (-not $token) {
    Write-Host "Como pegar seu token no jogo (leva 5 segundos):" -ForegroundColor Yellow
    Write-Host "  1. Abra o jogo logado no navegador: https://poke.idleworld.online/play" -ForegroundColor White
    Write-Host "  2. Pressione F12 -> Console" -ForegroundColor White
    Write-Host "  3. Cole o comando abaixo e aperte Enter:" -ForegroundColor White
    Write-Host '     copy(sessionStorage.getItem("pokeweb:tokens"))' -ForegroundColor Green
    Write-Host "     (Isso copia tanto o token de acesso quanto o refresh token permanente!)" -ForegroundColor Gray
    Write-Host ""
    Write-Host "Cole aqui abaixo e aperte Enter:" -ForegroundColor Cyan
    $inputRaw = Read-Host "Token / JSON"
    Parse-InputTokens $inputRaw
}

if (-not $token -or -not ($token -like "ey*.*.*")) {
    Write-Host ""
    Write-Host "[ERRO] Token invalido ou nao informado." -ForegroundColor Red
    return
}

Write-Host ""
Write-Host "Validando token com a API oficial do Poke Idle World..." -ForegroundColor Gray
$headers = @{
    'Authorization' = "Bearer $token"
    'User-Agent' = 'Mozilla/5.0'
}

try {
    $resp = Invoke-RestMethod -Uri "https://poke.idleworld.online/api/characters/me" -Headers $headers -Method Get
    if ($resp.character) {
        $name = $resp.character.name
        $level = $resp.character.level
        Write-Host "[OK] Treinador autenticado com sucesso: $name (Nivel $level)!" -ForegroundColor Green
    } else {
        Write-Host "[OK] Token validado com sucesso!" -ForegroundColor Green
    }
} catch {
    Write-Host "[AVISO] Nao foi possivel consultar o perfil na API, mas o token sera salvo." -ForegroundColor Yellow
}

try {
    if (Test-Path $accountsFile) {
        $raw = Get-Content $accountsFile -Raw
        $json = ConvertFrom-Json $raw
        $json[0].credentials.token = $token
        if ($refreshToken) {
            $json[0].credentials.refreshToken = $refreshToken
        }
        $out = ConvertTo-Json @($json) -Depth 10
        Set-Content -Path $accountsFile -Value $out -Encoding UTF8
        Write-Host "[OK] Token salvo com sucesso em accounts.json!" -ForegroundColor Green
    }
} catch {
    Write-Host "[ERRO] Erro ao salvar localmente: $_" -ForegroundColor Red
}

Write-Host ""
# Sincronização com VPS
if (Test-Path $keyFile) {
    $syncVPS = Read-Host "Deseja enviar o token para a VPS Oracle ($vpsHost) e reiniciar o bot la? (S/N) [Padrao: S]"
    if ($syncVPS -eq '' -or $syncVPS -eq 'S' -or $syncVPS -eq 's') {
        Write-Host "Sincronizando com a VPS..." -ForegroundColor Gray
        try {
            scp -i $keyFile -o StrictHostKeyChecking=no $accountsFile ubuntu@${vpsHost}:/home/ubuntu/pokeidleworld-bot/accounts.json
            ssh -i $keyFile -o StrictHostKeyChecking=no ubuntu@${vpsHost} "sudo systemctl restart pokeidleworld"
            Write-Host "[OK] VPS atualizada e servico 'pokeidleworld' reiniciado com sucesso!" -ForegroundColor Green
        } catch {
            Write-Host "[ERRO] Falha ao sincronizar com VPS: $_" -ForegroundColor Red
        }
    }
}

Write-Host ""
$start = Read-Host "Deseja iniciar o bot local agora? (S/N) [Padrao: N]"
if ($start -eq 'S' -or $start -eq 's') {
    $exe = Join-Path $root 'bot-poke.exe'
    if (Test-Path $exe) {
        Start-Process $exe
    } else {
        Start-Process 'go' -ArgumentList 'run .' -WorkingDirectory $root
    }
}
