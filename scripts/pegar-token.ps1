param()

Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "   POKE IDLE WORLD - CAPTURADOR AUTOMATICO DE TOKEN" -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host ""

$root = Resolve-Path "$PSScriptRoot\.."
$accountsFile = Join-Path $root "accounts.json"

$token = ""
try {
    $clip = [System.Windows.Forms.Clipboard]::GetText()
    if (-not $clip) {
        $clip = (Get-Clipboard -Raw)
    }
    if ($clip) {
        $clip = $clip.Trim()
        if ($clip -like "ey*.*.*") {
            Write-Host "[!] Token JWT detectado na area de transferencia (Ctrl+C)!" -ForegroundColor Green
            $len = [Math]::Min(35, $clip.Length)
            Write-Host "    $($clip.Substring(0, $len))..." -ForegroundColor Gray
            $confirm = Read-Host "Deseja utilizar este token? (S/N) [Padrao: S]"
            if ($confirm -eq '' -or $confirm -eq 'S' -or $confirm -eq 's') {
                $token = $clip
            }
        }
    }
} catch {}

if (-not $token) {
    Write-Host "Como pegar seu token no jogo (leva 5 segundos):" -ForegroundColor Yellow
    Write-Host "  1. Abra o jogo logado: https://poke.idleworld.online/play" -ForegroundColor White
    Write-Host "  2. Aperte F12 -> Console" -ForegroundColor White
    Write-Host "  3. Cole o comando abaixo e aperte Enter:" -ForegroundColor White
    Write-Host '     copy(JSON.parse(sessionStorage.getItem("pokeweb:tokens")).accessToken)' -ForegroundColor Green
    Write-Host ""
    Write-Host "Cole seu token abaixo e aperte Enter:" -ForegroundColor Cyan
    $inputToken = Read-Host "Token"
    if ($inputToken) {
        $token = $inputToken.Trim()
    }
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
        $out = ConvertTo-Json $json -Depth 10
        Set-Content -Path $accountsFile -Value $out -Encoding UTF8
        Write-Host "[OK] Token salvo com sucesso em accounts.json!" -ForegroundColor Green
    }
} catch {
    Write-Host "[ERRO] Erro ao salvar: $_" -ForegroundColor Red
}

Write-Host ""
$start = Read-Host "Deseja iniciar o bot agora? (S/N) [Padrao: S]"
if ($start -eq '' -or $start -eq 'S' -or $start -eq 's') {
    $exe = Join-Path $root 'bot-poke.exe'
    if (Test-Path $exe) {
        Start-Process $exe
    } else {
        Start-Process 'go' -ArgumentList 'run .' -WorkingDirectory $root
    }
}
