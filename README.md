# ⚡ Poke Idle World Bot (Go Native Engine)

Bot autônomo nativo em **Go (Golang)** de altíssima performance e baixíssimo consumo de memória (2 a 5 MB de RAM) para **[Poke Idle World](https://poke.idleworld.online/play)**.

🌐 **Command Center & Live Dashboard (GitHub Pages):**  
👉 **[https://kbelludoo.github.io/pokeidle-go/](https://kbelludoo.github.io/pokeidle-go/)**

---

## 🌟 Recursos do Bot

- 🚀 **WebSocket Direto com Sharding Automático:** Conexão nativa calculando o shard exato via hash DJB2 do claim `sub` do JWT (`wss://poke.idleworld.online/ws{shard}?token={token}&v=2`).
- 🎯 **Caça Inteligente por Fraqueza (Auto-Weakness):** Motor integrado de análise de tipos Pokémon. Calcula multiplicadores ofensivos (até **4.0x de Dano**) e seleciona automaticamente a melhor rota para maximizar EXP/h com menor risco.
- 🏰 **Automação de Guilda & Recrutamento no Chat:**
  - Envia anúncios automáticos de recrutamento no canal `help` do jogo a cada 15 minutos: `"Procuro guild ativa com bônus de XP..."`.
  - Escuta convites em tempo real e aceita instantaneamente a melhor guilda (+2.5% XP).
  - Detecta menções no chat para checagem imediata de convite.
  - Opção de doação automática de ouro para subir o nível da guilda.
- 🗺️ **Catálogo Completo Embutido:** 454 rotas/áreas de caça com níveis e tipos elementais calibrados.
- ⚔️ **Caçada Automatizada:** Mantém o personagem caçando continuamente, responde aos eventos de visão (`view`), e recupera automaticamente de `hunt-cooldown` ou `field-lost`.
- 🔴 **Auto-Catch Otimizado:** Lança Pokébolas instantaneamente em monstros derrotados (`pending`). Configurado para Ultra Ball (ID 3) em Shinies (Master Ball é bloqueada pelo jogo em Shinies).
- 💖 **Auto-Heal (Nurse Joy):** Aciona cura automática instantânea (`joy-heal`) caso a vida da equipe caia abaixo do limite configurável (default 30%) ou após ser derrotado.
- 🛒 **Auto-Buy de Pokébolas:** Reabastece Pokébolas via API HTTP caso o estoque caia abaixo do limite.
- 💰 **Auto-Sell de Comuns/Duplicados:** Vende pokémons repetidos sem equipe via `/api/game/pokemon/sell`.
- 🎁 **Coleta Diária:** Reivindica calendário diário e missões diárias de abate (`/api/game/daily`).
- 📊 **Monitoramento em Tempo Real:** Terminal com estatísticas limpas (RAM, Kills, Capturas, Ouro, EXP/h) e Web Dashboard embutido (Porta 3001).
- 🛡️ **Supervisor Anti-Crash:** Goroutines isoladas por conta com reconexão exponencial e proteção contra panic.

---

## 🚀 Como Configurar e Rodar

### 1. Configurar Conta
Copie `accounts.example.json` para `accounts.json`:

```json
[
  {
    "id": "acc-1",
    "name": "Meu Treinador",
    "enabled": true,
    "credentials": {
      "token": "SEU_ACCESS_TOKEN_AQUI",
      "refreshToken": ""
    },
    "settings": {
      "huntTarget": "caterpie",
      "autoHunt": true,
      "autoCatch": true,
      "ballId": 1,
      "shinyBallId": 5,
      "autoBuyBalls": true,
      "minBalls": 20,
      "buyBallQty": 50,
      "autoHeal": true,
      "healThresholdHp": 30,
      "autoDaily": true,
      "autoSell": true
    }
  }
]
```

#### 🔑 Como pegar seu Access Token:
1. Abra o jogo [https://poke.idleworld.online/play](https://poke.idleworld.online/play) no seu navegador e faça login.
2. Pressione **F12** (DevTools) e vá na aba **Console**.
3. Digite e pressione Enter:
   ```javascript
   JSON.parse(sessionStorage.getItem("pokeweb:tokens")).accessToken
   ```
4. Copie o token retornado e cole no campo `"token"` do `accounts.json` (ou injete diretamente no Web Dashboard pelo botão **"🔑 Injetar Token"**).

---

### 2. Rodar Localmente (Windows)

```bash
go run .
# ou execute o binário compilado:
.\bot-poke.exe
```

Acesse o painel web em: [http://localhost:3001](http://localhost:3001)

---

### 3. Deploy para Linux / VPS (Ubuntu)

Compile o binário para Linux:
```cmd
scripts\build-linux.bat
```

Envie para a VPS:
```bash
scp bot-poke-linux accounts.json ubuntu@IP_DA_VPS:/home/ubuntu/poke-bot/
scp scripts/bot.service ubuntu@IP_DA_VPS:/etc/systemd/system/poke-bot.service
```

Na VPS:
```bash
sudo systemctl daemon-reload
sudo systemctl enable poke-bot
sudo systemctl start poke-bot
sudo systemctl status poke-bot
```
