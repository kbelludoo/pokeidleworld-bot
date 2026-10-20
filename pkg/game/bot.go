package game

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bot-template-go/pkg/auth"
	"bot-template-go/pkg/engine"
	"bot-template-go/pkg/network"
)

var recruitPhrases = []string{
	"Procuro guild ativa com bÃ´nus de XP, nick: %s (farmando 24/7)",
	"Opa, alguma guilda ativa com vaga pra quem joga todo dia? nick: %s",
	"Procurando guild com bÃ´nus de xp, nick: %s on direto",
	"AlguÃ©m com vaga em guilda ativa? nick: %s (focado em xp)",
	"Procuro guild com bÃ´nus de XP, nick %s ativo direto aqui",
	"Salve galera, alguma guilda com vaga aberta? nick: %s",
	"Alguma guilda ativa recrutando? Nick: %s jogando full time",
	"Opa, guild ativa recrutando? Nick %s farmando direto aqui",
}

func getRandomRecruitMessage(tName string) string {
	idx := rand.Intn(len(recruitPhrases))
	return fmt.Sprintf(recruitPhrases[idx], tName)
}

func getRandomRecruitDelay() time.Duration {
	// Intervalo aleatÃ³rio de 2h a 8h (120 a 480 minutos) com variaÃ§Ã£o de segundos
	minutes := 120 + rand.Intn(361)
	seconds := rand.Intn(60)
	return time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
}

func formatDurationHuman(d time.Duration) string {
	if d <= 0 {
		return "agora"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

type Bot struct {
	config  AccountConfig
	mu      sync.RWMutex
	status  engine.AccountStatus
	socket  *network.SocketClient
	httpCli *network.HTTPClient
	catalog *Catalog

	// SessÃ£o e autenticaÃ§Ã£o
	token         string
	refreshToken  string
	targetSlug    string
	paused        bool
	isHunting     bool
	capturesCount int
	ballCounts    map[string]int

	// Fraqueza e vantagem de tipos
	activePokemon  string
	activeTypes    []string
	activeLevel    int
	targetTypes    []string
	offensiveMult  float64
	incomingRisk   float64
	availablePokes []PokemonItem

	// Guilda e Recrutamento
	guild             *GuildDetails
	invites           []GuildInvite
	lastGuildCheckAt  time.Time
	lastRecruitPingAt time.Time
	nextRecruitPingAt time.Time

	// BestiÃ¡rio e PokÃ©dex
	pokedexSpecies     map[int]PokedexSpeciesEntry
	lastPokedexCheckAt time.Time
	bestiarySkips      map[int]bool

	// Temporizadores e controle
	lastFieldAt   time.Time
	lastBuyBallAt time.Time
	lastDailyAt   time.Time
	ticketChan    chan string
	disconnectCh  chan error
	running       int32
}

func NewBot(cfg AccountConfig) *Bot {
	cat := GetGlobalCatalog()

	// Calibrar padrÃµes inteligentes
	if cfg.Settings.BallID <= 0 {
		cfg.Settings.BallID = 1 // Poke Ball
	}
	if cfg.Settings.ShinyBallID <= 0 || cfg.Settings.ShinyBallID == 5 {
		cfg.Settings.ShinyBallID = 3 // Ultra Ball (Master Ball ID 5 Ã© bloqueada pelo servidor em Shinies)
	}
	if cfg.Settings.MinBalls <= 0 {
		cfg.Settings.MinBalls = 20
	}
	if cfg.Settings.BuyBallQty <= 0 {
		cfg.Settings.BuyBallQty = 50
	}
	if cfg.Settings.HealThresholdHP <= 0 {
		cfg.Settings.HealThresholdHP = 30
	}
	if cfg.Settings.HuntTarget == "" || strings.ToLower(cfg.Settings.HuntTarget) == "auto" {
		cfg.Settings.AutoWeakness = true
		cfg.Settings.HuntTarget = "auto"
	}
	// Habilitar auto-guild por padrÃ£o se nÃ£o definido
	if !cfg.Settings.AutoGuildAccept {
		cfg.Settings.AutoGuildAccept = true
	}
	if !cfg.Settings.AutoGuildRecruit {
		cfg.Settings.AutoGuildRecruit = true
	}
	// Habilitar estratÃ©gia de BestiÃ¡rio por padrÃ£o
	if !cfg.Settings.AutoPokedexClaim {
		cfg.Settings.AutoPokedexClaim = true
	}
	if !cfg.Settings.PokedexFarmMode {
		cfg.Settings.PokedexFarmMode = true
	}

	st := engine.NewStatus(cfg.ID, cfg.Name)
	if cfg.Settings.AutoWeakness || cfg.Settings.HuntTarget == "auto" {
		st.CurrentArea = "Auto (Fraqueza Inteligente)"
	} else {
		st.CurrentArea = cat.GetHuntName(cfg.Settings.HuntTarget)
	}
	st.Extra["slug"] = cfg.Settings.HuntTarget
	st.Extra["autoWeakness"] = cfg.Settings.AutoWeakness
	st.Extra["pokedexFarmMode"] = cfg.Settings.PokedexFarmMode
	st.Extra["pokedexKills"] = "Carregando..."
	st.Extra["matchup"] = "Avaliando..."
	st.Extra["captures"] = 0
	st.Extra["balls"] = 0
	st.Extra["guild"] = "Sem Guilda (Buscando...)"
	st.Extra["guildTag"] = ""
	st.Extra["guildBonus"] = "+0.0% XP"

	httpCli := network.NewHTTPClientWithNetwork("https://poke.idleworld.online", 15*time.Second, cfg.Proxy, cfg.LocalIP)

	firstDelay := time.Duration(45+rand.Intn(75))*time.Minute + time.Duration(rand.Intn(60))*time.Second
	nextRecruitAt := time.Now().Add(firstDelay)

	return &Bot{
		config:            cfg,
		status:            st,
		httpCli:           httpCli,
		catalog:           cat,
		token:             cfg.Credentials.Token,
		refreshToken:      cfg.Credentials.RefreshToken,
		targetSlug:        cfg.Settings.HuntTarget,
		ballCounts:        make(map[string]int),
		pokedexSpecies:    make(map[int]PokedexSpeciesEntry),
		bestiarySkips:     make(map[int]bool),
		ticketChan:        make(chan string, 1),
		disconnectCh:      make(chan error, 1),
		nextRecruitPingAt: nextRecruitAt,
	}
}


func (b *Bot) ID() string {
	return b.config.ID
}

func (b *Bot) Name() string {
	return b.config.Name
}

// Init valida credenciais ou tokens iniciais
func (b *Bot) Init(ctx context.Context) error {
	b.updateStatus(func(s *engine.AccountStatus) {
		s.Status = "Verificando credenciais..."
	})

	if b.token != "" {
		b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
	}

	// 1. Se possuir token, valida via /api/characters/me
	if b.token != "" {
		var charResp CharacterMeResponse
		err := b.httpCli.GetJSON("/api/characters/me", &charResp)
		if err == nil && charResp.Character != nil {
			ch := charResp.Character
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Name = ch.Name
				s.Level = ch.Level
				s.Status = "Token validado"
				s.Extra["trainerName"] = ch.Name
				s.Extra["lookType"] = ch.LookType
			})
			log.Printf("[%s] Autenticado com sucesso como treinador '%s' (NÃ­vel %d)", b.ID(), ch.Name, ch.Level)
			return nil
		}

		// Se o token falhou e temos refreshToken, tenta renovar
		if b.refreshToken != "" {
			log.Printf("[%s] Token expirado. Tentando renovaÃ§Ã£o via /api/auth/refresh...", b.ID())
			if b.refreshAccessToken() {
				return nil
			}
		}
	}

	// 2. Se o usuÃ¡rio forneceu refreshToken diretamente
	if b.token == "" && b.refreshToken != "" {
		log.Printf("[%s] Obtendo novo accessToken a partir do refreshToken...", b.ID())
		if b.refreshAccessToken() {
			return nil
		}
	}

	// 3. Se possuir e-mail e senha, tenta login automatizado via navegador local (CDP)
	if b.config.Credentials.Email != "" && b.config.Credentials.Password != "" {
		log.Printf("[%s] Tentando login automatizado via navegador local (CDP) para %s...", b.ID(), b.config.Credentials.Email)
		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "Autenticando via CDP..."
		})
		tokens, err := auth.LoginViaBrowserCDP(b.config.Credentials.Email, b.config.Credentials.Password)
		if err == nil && tokens != nil && tokens.AccessToken != "" {
			b.token = tokens.AccessToken
			b.refreshToken = tokens.RefreshToken
			b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
			log.Printf("[%s] Sucesso! Token de acesso capturado via navegador.", b.ID())

			// Salva permanentemente no accounts.json
			b.saveTokensToAccountsFile(tokens.AccessToken, tokens.RefreshToken)

			var charResp CharacterMeResponse
			if err := b.httpCli.GetJSON("/api/characters/me", &charResp); err == nil && charResp.Character != nil {
				ch := charResp.Character
				b.updateStatus(func(s *engine.AccountStatus) {
					s.Name = ch.Name
					s.Level = ch.Level
					s.Status = "Token validado"
					s.Extra["trainerName"] = ch.Name
					s.Extra["lookType"] = ch.LookType
				})
			}
			return nil
		}

		log.Printf("[%s] Login automÃ¡tico via CDP nÃ£o completou (%v).", b.ID(), err)
		log.Printf("[%s] ðŸ’¡ AVISO: O login web do Poke Idle World exige Turnstile Anti-RobÃ´.", b.ID())
		log.Printf("[%s] ðŸ‘‰ Para conectar imediatamente, pegue o accessToken no navegador (DevTools > SessionStorage > pokeweb:tokens) e cole em 'credentials.token' no accounts.json ou envie via Dashboard: http://localhost:3001/api/ticket", b.ID())
		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "Aguardando token/ticket"
		})
	} else if b.token == "" {
		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "Sem credenciais"
		})
	}

	return nil
}

// Run executa a conexÃ£o com o WebSocket e os loops de jogo
func (b *Bot) Run(ctx context.Context) error {
	atomic.StoreInt32(&b.running, 1)
	defer atomic.StoreInt32(&b.running, 0)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Garante que temos um token antes de abrir o socket
		if b.token == "" {
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Aguardando token..."
			})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case t := <-b.ticketChan:
				b.token = t
				b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
				log.Printf("[%s] Token recebido via SupplyTicket. Iniciando conexÃ£o...", b.ID())
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// Calcula a URL do WebSocket com shard e hash DJB2
		wsURL := b.computeWebSocketURL(b.token)

		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "Conectando ao WebSocket..."
		})

		headers := make(map[string][]string)
		headers["Origin"] = []string{"https://poke.idleworld.online"}
		headers["User-Agent"] = []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"}

		b.disconnectCh = make(chan error, 1)
		socket := network.NewSocketClientWithNetwork(
			wsURL,
			headers,
			b.config.Proxy,
			b.config.LocalIP,
			b.handleMessage,
			func(err error) {
				select {
				case b.disconnectCh <- err:
				default:
				}
			},
		)

		if err := socket.Connect(); err != nil {
			log.Printf("[%s] Falha ao conectar ao WebSocket (%s): %v", b.ID(), wsURL, err)
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Erro de conexão (re-tentando...)"
			})
			if (strings.Contains(err.Error(), "4001") || strings.Contains(err.Error(), "unauthorized")) && b.refreshToken != "" {
				log.Printf("[%s] Token inválido/expirado detectado (4001). Tentando renovar via refreshToken...", b.ID())
				if b.refreshAccessToken() {
					log.Printf("[%s] 🎉 Token renovado com sucesso! Reconectando imediatamente...", b.ID())
					continue
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
				continue
			}
		}

		b.mu.Lock()
		b.socket = socket
		b.lastFieldAt = time.Now()
		b.mu.Unlock()

		b.updateStatus(func(s *engine.AccountStatus) {
			s.Status = "Conectado. Inicializando..."
		})
		b.addLog("Conectado ao servidor Poke Idle World.")

		// Envia pacotes iniciais de sincronizaÃ§Ã£o
		_ = socket.SendJSON(map[string]any{"type": "pokes-get"})
		_ = socket.SendJSON(map[string]any{"type": "balls-get"})
		_ = socket.SendJSON(map[string]any{"type": "autohelper-get"})
		_ = socket.SendJSON(map[string]any{"type": "inv-get"})
		_ = socket.SendJSON(map[string]any{"type": "analyzer-get"})
		_ = socket.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
		go b.checkAndClaimPokedex()

		// Entra na Ã¡rea de caÃ§a se AutoHunt estiver habilitado e rota jÃ¡ estiver definida
		b.mu.RLock()
		currentSlug := b.targetSlug
		b.mu.RUnlock()
		if b.config.Settings.AutoHunt && !b.paused && currentSlug != "" && currentSlug != "auto" {
			b.sendEnterHunt(false)
		}

		// Loop de supervisÃ£o interna da conexÃ£o
		runErr := b.superviseSession(ctx)
		socket.Close()

		if runErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}

		reconnectWait := 3 * time.Second
		if runErr != nil && strings.Contains(runErr.Error(), "4005") {
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Sessão Ativa no Navegador (pausa 30s)"
			})
			b.addLog("Sessão aberta em outro dispositivo/aba (código 4005). Aguardando 30s para não derrubar o jogador...")
			log.Printf("[%s] Sessão aberta no navegador (close 4005). Aguardando 30 segundos...", b.ID())
			reconnectWait = 30 * time.Second
		} else if runErr != nil && (strings.Contains(runErr.Error(), "4001") || strings.Contains(runErr.Error(), "unauthorized")) {
			log.Printf("[%s] Token expirado durante a sessão (close 4001). Tentando renovar via refreshToken...", b.ID())
			if b.refreshAccessToken() {
				log.Printf("[%s] 🎉 Token renovado com sucesso após 4001! Reconectando imediatamente...", b.ID())
				reconnectWait = 1 * time.Second
			} else {
				reconnectWait = 5 * time.Second
			}
		} else {
			log.Printf("[%s] Conexão encerrada. Reconectando em 3 segundos...", b.ID())
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectWait):
		}
	}
}

// superviseSession monitora keepalive, watchdog de caÃ§a e tarefas periÃ³dicas
func (b *Bot) superviseSession(ctx context.Context) error {
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	watchdogTicker := time.NewTicker(4 * time.Second)
	defer watchdogTicker.Stop()

	guildTicker := time.NewTicker(30 * time.Second)
	defer guildTicker.Stop()

	dailyTicker := time.NewTicker(30 * time.Minute)
	defer dailyTicker.Stop()

	pokedexTicker := time.NewTicker(35 * time.Second)
	defer pokedexTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-b.disconnectCh:
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Desconectado"
			})
			b.addLog(fmt.Sprintf("ConexÃ£o WebSocket interrompida: %v", err))
			return err

		case newTicket := <-b.ticketChan:
			b.token = newTicket
			b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
			b.addLog("Novo token aplicado. Reiniciando sessÃ£o...")
			return fmt.Errorf("token atualizado")

		case <-pingTicker.C:
			b.mu.RLock()
			s := b.socket
			b.mu.RUnlock()
			if s != nil && s.IsConnected() {
				_ = s.SendJSON(map[string]any{"type": "ping"})
			}

		case <-watchdogTicker.C:
			b.mu.RLock()
			s := b.socket
			lastField := b.lastFieldAt
			hunting := b.isHunting
			paused := b.paused
			autoHunt := b.config.Settings.AutoHunt
			b.mu.RUnlock()

			if s != nil && s.IsConnected() && autoHunt && !paused {
				// Se estiver caÃ§ando e sem atualizaÃ§Ã£o do campo por mais de 12 segundos, re-envia enter-hunt
				if hunting && time.Since(lastField) > 12*time.Second {
					b.sendEnterHunt(true)
				}
				// Solicita atualizaÃ§Ã£o periÃ³dica do analyzer a cada tick do watchdog
				_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
			}

		case <-guildTicker.C:
			b.mu.RLock()
			s := b.socket
			currentGuild := b.guild
			autoRecruit := b.config.Settings.AutoGuildRecruit
			nextPing := b.nextRecruitPingAt
			tName := b.status.Name
			if tName == "" {
				tName = b.Name()
			}
			b.mu.RUnlock()

			if s != nil && s.IsConnected() {
				// 1. Atualiza dados e lista da guilda a cada 30 segundos
				_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})

				// 2. Se estiver sem guilda e autoRecruit estiver ativo, envia mensagem no canal help com intervalo aleatÃ³rio entre 2h e 8h
				if currentGuild == nil && autoRecruit && (nextPing.IsZero() || time.Now().After(nextPing)) {
					nextDelay := getRandomRecruitDelay()
					b.mu.Lock()
					b.lastRecruitPingAt = time.Now()
					b.nextRecruitPingAt = time.Now().Add(nextDelay)
					b.mu.Unlock()

					msg := getRandomRecruitMessage(tName)
					_ = s.SendJSON(SendChatPacket{
						Type:    "send",
						Channel: "help",
						Body:    msg,
					})
					b.addLog(fmt.Sprintf("ðŸ“¢ [Guilda] AnÃºncio aleatÃ³rio enviado no chat: \"%s\" (prÃ³ximo sorteado em %s)", msg, formatDurationHuman(nextDelay)))
				}
			}

		case <-dailyTicker.C:
			if b.config.Settings.AutoDaily {
				go b.claimDailyRewards()
			}

		case <-pokedexTicker.C:
			go b.checkAndClaimPokedex()
		}
	}
}

// handleMessage processa cada pacote recebido do servidor WebSocket
func (b *Bot) handleMessage(data []byte) {
	var base BaseSocketMessage
	if err := json.Unmarshal(data, &base); err != nil {
		return
	}

	switch base.Type {
	case "field-init":
		var ev FieldInitEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.isHunting = true
			b.lastFieldAt = time.Now()
			b.targetSlug = ev.Slug
			s := b.socket

			targetTypes := b.catalog.GetHuntTypes(ev.Slug)
			b.targetTypes = targetTypes
			activeTypes := b.activeTypes
			if len(activeTypes) > 0 && len(targetTypes) > 0 {
				b.offensiveMult = GetBestOffensiveMultiplier(activeTypes, targetTypes)
				b.incomingRisk = GetIncomingRisk(activeTypes, targetTypes)
			}
			offMult := b.offensiveMult
			risk := b.incomingRisk
			b.mu.Unlock()

			if s != nil {
				// Responde com o campo de visÃ£o exigido pelo servidor
				_ = s.SendJSON(ViewPacket{Type: "view", HC: 15, HR: 9})
			}

			areaName := b.catalog.GetHuntName(ev.Slug)
			huntLevel := 0
			if h, ok := b.catalog.GetHunt(ev.Slug); ok {
				huntLevel = h.Level
			}
			desc := FormatMatchupDescription(offMult, risk, huntLevel)
			b.updateStatus(func(st *engine.AccountStatus) {
				st.Status = "hunting"
				st.CurrentArea = areaName
				st.Extra["slug"] = ev.Slug
				st.Extra["huntLevel"] = huntLevel
				st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
				st.Extra["matchupDetail"] = desc
				st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
			})
			b.addLog(fmt.Sprintf("Entrou na rota de caÃ§a: %s (%s) | %s", areaName, ev.Slug, desc))
		}

	case "field":
		var ev FieldEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.lastFieldAt = time.Now()
			b.isHunting = true
			b.mu.Unlock()

			b.updateStatus(func(st *engine.AccountStatus) {
				st.Status = "hunting"
				if ev.Hero.MaxHP > 0 {
					st.HP = ev.Hero.HP
					st.MaxHP = ev.Hero.MaxHP
				}
				if ev.Hero.Level > 0 {
					st.Extra["pokemonLevel"] = ev.Hero.Level
				}
			})

			// Se o PokÃ©mon ativo subiu de nÃ­vel, registra log
			b.mu.Lock()
			oldPokeLevel := b.activeLevel
			if ev.Hero.Level > 0 {
				b.activeLevel = ev.Hero.Level
			}
			b.mu.Unlock()
			if oldPokeLevel > 0 && ev.Hero.Level > oldPokeLevel {
				b.addLog(fmt.Sprintf("ðŸŽ‰ PokÃ©mon subiu para o NÃ­vel %d!", ev.Hero.Level))
			}

			// VerificaÃ§Ã£o de vida baixa para cura automÃ¡tica
			if ev.Hero.MaxHP > 0 && b.config.Settings.AutoHeal {
				hpPct := (ev.Hero.HP * 100) / ev.Hero.MaxHP
				if hpPct <= b.config.Settings.HealThresholdHP {
					b.mu.RLock()
					s := b.socket
					b.mu.RUnlock()
					if s != nil {
						_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
						b.addLog(fmt.Sprintf("Vida crÃ­tica (%d%%). Nurse Joy curou a equipe!", hpPct))
					}
				}
			}
		}

	case "field-kill":
		kCount := 0
		b.updateStatus(func(st *engine.AccountStatus) {
			st.Kills++
			kCount = st.Kills
		})
		b.mu.RLock()
		s := b.socket
		currentSlug := b.targetSlug
		b.mu.RUnlock()
		if s != nil {
			_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
		}

		// Rastreia e atualiza localmente os abates da espÃ©cie caÃ§ada
		hit100 := false
		if currentSlug != "" {
			spID := b.catalog.GetSpeciesIDByName(currentSlug)
			if spID > 0 {
				b.mu.Lock()
				if b.pokedexSpecies == nil {
					b.pokedexSpecies = make(map[int]PokedexSpeciesEntry)
				}
				entry := b.pokedexSpecies[spID]
				entry.ID = spID
				entry.Kills++
				if entry.Kills >= 100 && !entry.Claimed {
					entry.CanClaim = true
					hit100 = true
				}
				b.pokedexSpecies[spID] = entry
				curKills := entry.Kills
				b.mu.Unlock()

				b.updateStatus(func(st *engine.AccountStatus) {
					st.Extra["pokedexKills"] = fmt.Sprintf("%d/100", curKills)
				})
			}
		}

		// Se atingiu a meta exata de 100 abates ou mÃºltiplo de 10, consulta/resgata bÃ´nus na PokÃ©dex
		if hit100 || (kCount > 0 && kCount%10 == 0) {
			go b.checkAndClaimPokedex()
		}

	case "field-lost":
		b.addLog("A equipe foi derrotada em batalha. Curando com Nurse Joy e reiniciando caÃ§a...")
		b.mu.RLock()
		s := b.socket
		b.mu.RUnlock()
		if s != nil {
			if b.config.Settings.AutoHeal {
				_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
			}
			time.Sleep(1200 * time.Millisecond)
			if b.config.Settings.AutoHunt && !b.paused {
				b.sendEnterHunt(true)
			}
		}

	case "field-none":
		b.mu.Lock()
		b.isHunting = false
		b.mu.Unlock()
		b.updateStatus(func(st *engine.AccountStatus) {
			if !b.paused {
				st.Status = "idle (fora de caÃ§a)"
			}
		})
		if b.config.Settings.AutoHunt && !b.paused {
			time.Sleep(1 * time.Second)
			b.sendEnterHunt(true)
		}

	case "hunt-cooldown", "fishing-cooldown":
		var ev HuntCooldownEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			waitMs := ev.MS
			if waitMs <= 0 {
				waitMs = 1000
			}
			go func(d time.Duration) {
				time.Sleep(d + 100*time.Millisecond)
				b.mu.RLock()
				auto := b.config.Settings.AutoHunt && !b.paused
				b.mu.RUnlock()
				if auto {
					b.sendEnterHunt(true)
				}
			}(time.Duration(waitMs) * time.Millisecond)
		}

	case "pending":
		var ev PendingEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			if b.config.Settings.AutoCatch && len(ev.List) > 0 {
				b.mu.RLock()
				s := b.socket
				ballID := b.config.Settings.BallID
				shinyBallID := b.config.Settings.ShinyBallID
				b.mu.RUnlock()

				if s != nil {
					for _, item := range ev.List {
						chosenBall := ballID
						if item.Shiny {
							chosenBall = shinyBallID
							if chosenBall == 5 {
								chosenBall = 3 // Fallback para Ultra Ball (Master Ball Ã© bloqueada para Shinies)
							}
						}
						_ = s.SendJSON(CatchPacket{
							Type:      "catch",
							PendingID: item.ID,
							BallID:    chosenBall,
						})
						b.addLog(fmt.Sprintf("PokÃ©bola (ID %d) lanÃ§ada em monstro pendente #%v (Shiny: %v)", chosenBall, item.SpeciesID, item.Shiny))
					}
				}
			}
		}

	case "catch-result":
		var ev CatchResultEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			if ev.Success {
				b.capturesCount++
				b.updateStatus(func(st *engine.AccountStatus) {
					st.Extra["captures"] = b.capturesCount
				})
				b.addLog(fmt.Sprintf("âœ¨ Captura bem-sucedida com PokÃ©bola ID %d! Total: %d", ev.BallID, b.capturesCount))
			} else {
				b.addLog(fmt.Sprintf("PokÃ©bola ID %d falhou na captura.", ev.BallID))
			}
			b.mu.RLock()
			s := b.socket
			b.mu.RUnlock()
			if s != nil {
				_ = s.SendJSON(map[string]any{"type": "balls-get"})
			}
		}

	case "balls":
		var ev BallsEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.ballCounts = ev.Counts
			ballID := b.config.Settings.BallID
			b.mu.Unlock()

			count := ev.Counts[fmt.Sprintf("%d", ballID)]
			b.updateStatus(func(st *engine.AccountStatus) {
				st.Extra["balls"] = count
				if ev.Gold > 0 {
					st.Gold = ev.Gold
				}
			})

			// Auto compra de PokÃ©bolas se estiver abaixo do mÃ­nimo
			if b.config.Settings.AutoBuyBalls && count < b.config.Settings.MinBalls {
				b.mu.Lock()
				shouldBuy := time.Since(b.lastBuyBallAt) > 30*time.Second
				if shouldBuy {
					b.lastBuyBallAt = time.Now()
				}
				b.mu.Unlock()

				if shouldBuy {
					go b.buyBalls(ballID, b.config.Settings.BuyBallQty)
				}
			}
		}

	case "analyzer":
		var ev AnalyzerEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.updateStatus(func(st *engine.AccountStatus) {
				st.Kills = ev.Kills
				st.DurationSec = ev.Seconds
				if ev.Balance != 0 {
					st.Gold = ev.Balance
				}
				if ev.Seconds > 0 {
					st.ExpPerHour = float64(ev.XPGained) * 3600.0 / float64(ev.Seconds)
				}
				st.Extra["captures"] = ev.Captures
				st.Extra["shinyCaptures"] = ev.ShinyCaptures
				st.Extra["lootGold"] = ev.LootGold
				st.Extra["lootItems"] = ev.LootItems
			})
		}

	case "pokes":
		var ev PokesEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.availablePokes = ev.List
			b.mu.Unlock()

			b.updateStatus(func(st *engine.AccountStatus) {
				st.Extra["pokeCount"] = len(ev.List)
			})

			b.mu.RLock()
			s := b.socket
			b.mu.RUnlock()

			hasTeam := false
			teamNeedsHeal := false
			var activeP *PokemonItem
			for i := range ev.List {
				if ev.List[i].Team {
					hasTeam = true
					activeP = &ev.List[i]
					if activeP.HP <= 0 {
						teamNeedsHeal = true
					}
					break
				}
			}

			if activeP != nil {
				types := b.catalog.GetPokemonTypes(activeP.SpeciesID, activeP.Name)
				b.mu.Lock()
				b.activePokemon = activeP.Name
				b.activeLevel = activeP.Level
				b.activeTypes = types
				targetSlug := b.targetSlug
				b.mu.Unlock()

				typeStr := strings.Join(types, " / ")
				b.updateStatus(func(st *engine.AccountStatus) {
					st.HP = activeP.HP
					st.MaxHP = activeP.MaxHP
					st.Extra["activePokemon"] = activeP.Name
					st.Extra["pokemonLevel"] = activeP.Level
					st.Extra["pokemonTypes"] = typeStr
				})

				// Avalia fraquezas com base no PokÃ©mon da equipe
				b.evaluateAndSwitchWeaknessHunt(targetSlug == "auto" || targetSlug == "")
			}

			if !hasTeam && len(ev.List) > 0 && s != nil {
				// Invoca o primeiro PokÃ©mon disponÃ­vel para a equipe
				p := ev.List[0]
				types := b.catalog.GetPokemonTypes(p.SpeciesID, p.Name)
				b.mu.Lock()
				b.activePokemon = p.Name
				b.activeLevel = p.Level
				b.activeTypes = types
				b.mu.Unlock()

				_ = s.SendJSON(SummonPacket{Type: "poke-summon", PokeID: p.ID})
				b.addLog(fmt.Sprintf("Invocando PokÃ©mon %s (Lv. %d) para a equipe...", p.Name, p.Level))
				time.Sleep(500 * time.Millisecond)
				_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
				time.Sleep(500 * time.Millisecond)

				b.evaluateAndSwitchWeaknessHunt(true)
			} else if teamNeedsHeal && s != nil && b.config.Settings.AutoHeal {
				_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
				b.addLog("PokÃ©mon da equipe sem vida. Curado com Nurse Joy!")
			}

			if b.config.Settings.AutoSell {
				go b.checkAutoSell(ev.List)
			}
		}

	case "guild":
		var ev GuildEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.lastGuildCheckAt = time.Now()
			b.guild = ev.Guild
			b.invites = ev.Invites
			s := b.socket
			gold := ev.MyGold
			autoAccept := b.config.Settings.AutoGuildAccept
			autoDonate := b.config.Settings.AutoGuildDonate
			b.mu.Unlock()

			if ev.Guild != nil {
				g := ev.Guild
				bonusStr := fmt.Sprintf("+%.1f%% XP", g.BonusPct)
				b.updateStatus(func(st *engine.AccountStatus) {
					st.Extra["guild"] = fmt.Sprintf("[%s] %s", g.Tag, g.Name)
					st.Extra["guildName"] = g.Name
					st.Extra["guildTag"] = g.Tag
					st.Extra["guildBonus"] = bonusStr
					st.Extra["guildMembers"] = fmt.Sprintf("%d/%d", len(g.Members), g.Slots)
					st.Extra["guildPoints"] = g.Points
					st.Extra["guildTier"] = g.Tier
				})

				// Se tiver ouro sobrando e AutoDonate ativo, doa para a guilda ajudar a abrir vagas
				if autoDonate && gold > 20000 && s != nil {
					donateAmt := int(gold - 10000)
					if donateAmt > 50000 {
						donateAmt = 50000
					}
					if donateAmt > 0 {
						_ = s.SendJSON(GuildActionPacket{
							Type:   "guild-action",
							Action: "donate",
							Amount: donateAmt,
						})
						b.addLog(fmt.Sprintf("ðŸ’° [Guilda] Doado %d de ouro para a guilda %s [%s]!", donateAmt, g.Name, g.Tag))
					}
				}
			} else {
				b.mu.RLock()
				nextPing := b.nextRecruitPingAt
				b.mu.RUnlock()
				nextStr := "em breve"
				if !nextPing.IsZero() && time.Now().Before(nextPing) {
					nextStr = formatDurationHuman(time.Until(nextPing))
				}
				b.updateStatus(func(st *engine.AccountStatus) {
					st.Extra["guild"] = "Sem Guilda (Aguardando convite)"
					st.Extra["guildName"] = "Nenhuma"
					st.Extra["guildTag"] = ""
					st.Extra["guildBonus"] = "+0.0% XP"
					st.Extra["nextRecruit"] = nextStr
				})

				// Se recebemos convites e autoAccept estÃ¡ ativo
				if len(ev.Invites) > 0 && autoAccept && s != nil {
					bestInvite := ev.Invites[0]
					b.addLog(fmt.Sprintf("ðŸ“© [Guilda] Recebido convite da guilda %s [%s] (convidado por: %s)!",
						bestInvite.GuildName, bestInvite.GuildTag, bestInvite.InvitedBy))

					// Envia aceitaÃ§Ã£o imediata com o pacote oficial comprovado
					_ = s.SendJSON(GuildActionPacket{
						Type:     "guild-action",
						Action:   "respond",
						InviteID: bestInvite.ID,
						Accept:   true,
					})
					b.addLog(fmt.Sprintf("ðŸŽ‰ [Guilda] Convite aceito! Entramos na guilda %s [%s]!",
						bestInvite.GuildName, bestInvite.GuildTag))

					time.Sleep(1 * time.Second)
					_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
				}
			}
		}

	case "chat":
		var ev ChatEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			tName := strings.ToLower(b.status.Name)
			if tName == "" {
				tName = strings.ToLower(b.Name())
			}
			bodyLower := strings.ToLower(ev.Msg.Body)
			// Se alguÃ©m nos mencionou ou convidou no chat
			if tName != "" && strings.Contains(bodyLower, tName) {
				b.addLog(fmt.Sprintf("ðŸ’¬ [Chat %s] %s: %s", ev.Msg.Channel, ev.Msg.FromName, ev.Msg.Body))
				// Imediatamente atualiza guild-get para pegar o convite
				b.mu.RLock()
				s := b.socket
				b.mu.RUnlock()
				if s != nil {
					_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
				}
			}
		}

		case "amigos":
		var ev struct {
			Lista []struct {
				ID        any    `json:"id"`
				Nick      string `json:"nick"`
				CoinsSent bool   `json:"coinsEnviadasHoje"`
			} `json:"lista"`
		}
		if err := json.Unmarshal(data, &ev); err == nil && b.config.Settings.AutoFriendCoins {
			b.mu.RLock()
			s := b.socket
			b.mu.RUnlock()
			if s != nil && s.IsConnected() {
				for _, a := range ev.Lista {
					if !a.CoinsSent {
						_ = s.SendJSON(FriendCoinsPacket{Type: "amigo.coins", AmigoID: a.ID, Valor: 1})
						time.Sleep(200 * time.Millisecond)
					}
				}
			}
		}

	case "evolucao":
		var ev struct {
			Nome string `json:"nome"`
			De   string `json:"de"`
		}
		if err := json.Unmarshal(data, &ev); err == nil && ev.Nome != "" {
			b.addLog(fmt.Sprintf("🎉 [Evolução Concluída] %s evoluiu com sucesso para %s!", ev.De, ev.Nome))
			b.mu.RLock()
			s := b.socket
			b.mu.RUnlock()
			if s != nil {
				_ = s.SendJSON(map[string]any{"type": "pokes-get"})
			}
		}

	case "passe":
		b.addLog("📜 [Passe de Batalha] Recompensa diária coletada com sucesso!")

	case "ginasio":
		var ev struct {
			Venceu bool   `json:"venceu"`
			Tipo   string `json:"tipo"`
		}
		if err := json.Unmarshal(data, &ev); err == nil && ev.Tipo != "" {
			if ev.Venceu {
				b.addLog(fmt.Sprintf("👑 [Ginásio %s] Vitória! Buff permanente de +10%% de dano ativo!", ev.Tipo))
			}
		}

	case "error":
		var ev struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &ev)
		if ev.Message != "" {
			b.addLog("Aviso do jogo: " + ev.Message)
		} else {
			b.addLog("Mensagem do jogo: " + string(data))
		}
	}
}

// evaluateAndSwitchWeaknessHunt analisa os tipos do PokÃ©mon ativo e escolhe a rota onde o inimigo tem maior fraqueza
func (b *Bot) evaluateAndSwitchWeaknessHunt(forceEnter bool) {
	b.mu.Lock()
	activeTypes := b.activeTypes
	activeName := b.activePokemon
	currentSlug := b.targetSlug
	autoWeakness := b.config.Settings.AutoWeakness || currentSlug == "auto" || currentSlug == ""
	playerLevel := b.status.Level
	if playerLevel <= 0 {
		playerLevel = b.activeLevel
	}
	if playerLevel <= 0 {
		playerLevel = 1
	}
	b.mu.Unlock()

	if len(activeTypes) == 0 {
		return
	}

	if !autoWeakness && currentSlug != "" && currentSlug != "auto" {
		// Modo manual: calcula eficÃ¡cia do alvo atual para exibiÃ§Ã£o
		targetTypes := b.catalog.GetHuntTypes(currentSlug)
		huntLevel := 0
		if h, ok := b.catalog.GetHunt(currentSlug); ok {
			huntLevel = h.Level
		}
		offMult := GetBestOffensiveMultiplier(activeTypes, targetTypes)
		risk := GetIncomingRisk(activeTypes, targetTypes)
		desc := FormatMatchupDescription(offMult, risk, huntLevel)

		b.mu.Lock()
		b.offensiveMult = offMult
		b.incomingRisk = risk
		b.targetTypes = targetTypes
		b.mu.Unlock()

		b.updateStatus(func(st *engine.AccountStatus) {
			st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
			st.Extra["matchupDetail"] = desc
			st.Extra["huntLevel"] = huntLevel
			st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
		})
		return
	}

	b.mu.RLock()
	dexEntries := make(map[int]PokedexSpeciesEntry)
	for k, v := range b.pokedexSpecies {
		dexEntries[k] = v
	}
	pokedexMode := b.config.Settings.PokedexFarmMode
	skips := make(map[int]bool)
	for k, v := range b.bestiarySkips {
		skips[k] = v
	}
	pokes := make([]PokemonItem, len(b.availablePokes))
	copy(pokes, b.availablePokes)
	b.mu.RUnlock()

	// Gera fila completa do BestiÃ¡rio com base no catÃ¡logo e nas fraquezas elementais
	fullQueue := b.catalog.GetBestiaryQueue(activeTypes, playerLevel, dexEntries)
	totalAccessible := len(fullQueue)
	totalCompleted := 0
	for _, it := range fullQueue {
		if it.Claimed || it.Kills >= 100 {
			totalCompleted++
		}
	}

	var bestHunt CatalogHunt
	var offMult, risk float64
	var found bool
	var activeTarget *BestiaryQueueItem

	if pokedexMode && len(fullQueue) > 0 {
		// 1. Verifica o primeiro alvo pendente do PokÃ©mon ativo atual
		for _, it := range fullQueue {
			if it.Score > 0 && !skips[it.SpeciesID] {
				cpy := it
				activeTarget = &cpy
				break
			}
		}

		// 2. Se o PokÃ©mon atual NÃƒO possui alvo 4.0x (ou se o alvo atual for < 4.0x),
		// verifica se OUTRO PokÃ©mon na mochila possui alvo 4.0x no BestiÃ¡rio para troca!
		if (activeTarget == nil || activeTarget.Offensive < 4.0) && len(pokes) > 1 {
			for _, p := range pokes {
				if p.Name == activeName {
					continue
				}
				pTypes := b.catalog.GetPokemonTypes(p.SpeciesID, p.Name)
				if len(pTypes) == 0 {
					continue
				}
				pQueue := b.catalog.GetBestiaryQueue(pTypes, playerLevel, dexEntries)
				for _, it := range pQueue {
					if it.Offensive >= 4.0 && it.Score > 0 && !skips[it.SpeciesID] {
						// Encontrou alvo 4.0x para outro PokÃ©mon da equipe!
						b.mu.RLock()
						s := b.socket
						b.mu.RUnlock()
						if s != nil && s.IsConnected() {
							_ = s.SendJSON(SummonPacket{Type: "poke-summon", PokeID: p.ID})
							time.Sleep(300 * time.Millisecond)
							_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
							b.addLog(fmt.Sprintf("ðŸ”„ [Troca EstratÃ©gica 4.0x] Invocando %s (Lv. %d) [%s] para explorar fraqueza 4.0x contra %s (Lv. %d)!",
								p.Name, p.Level, strings.Join(pTypes, "/"), it.Name, it.Level))
						}

						b.mu.Lock()
						b.activePokemon = p.Name
						b.activeLevel = p.Level
						b.activeTypes = pTypes
						b.mu.Unlock()

						activeName = p.Name
						activeTypes = pTypes
						fullQueue = pQueue
						cpy := it
						activeTarget = &cpy
						if h, ok := b.catalog.GetHunt(it.Slug); ok {
							bestHunt = h
							offMult = it.Offensive
							risk = it.Risk
							found = true
						}
						break
					}
				}
				if found && activeTarget != nil && activeTarget.Offensive >= 4.0 {
					break
				}
			}
		}

		// 3. Se todos os pendentes foram pulados manualmente, reseta pulos e escolhe o topo
		if activeTarget == nil {
			for _, it := range fullQueue {
				if it.Score > 0 {
					cpy := it
					activeTarget = &cpy
					b.mu.Lock()
					b.bestiarySkips = make(map[int]bool)
					b.mu.Unlock()
					break
				}
			}
		}

		if activeTarget != nil && !found {
			if h, ok := b.catalog.GetHunt(activeTarget.Slug); ok {
				bestHunt = h
				offMult = activeTarget.Offensive
				risk = activeTarget.Risk
				found = true
			}
		}
	}

	// Fallback para caÃ§ada de maior XP se nÃ£o estiver em modo BestiÃ¡rio ou todos jÃ¡ foram concluÃ­dos
	if !found {
		bestHunt, offMult, risk, found = b.catalog.FindBestHunt(activeTypes, playerLevel, dexEntries, false)
		if !found {
			return
		}
	}

	targetTypes := b.catalog.GetHuntTypes(bestHunt.Slug)
	desc := FormatMatchupDescription(offMult, risk, bestHunt.Level)

	targetSpID := b.catalog.GetSpeciesIDByName(bestHunt.Slug)
	killsText := "0/100"
	if entry, ok := dexEntries[targetSpID]; ok {
		killsText = fmt.Sprintf("%d/100", entry.Kills)
	}

	// Fila dos prÃ³ximos alvos do BestiÃ¡rio para o painel web
	var topQueue []BestiaryQueueItem
	for _, it := range fullQueue {
		if it.Score > 0 {
			topQueue = append(topQueue, it)
			if len(topQueue) >= 8 {
				break
			}
		}
	}

	// Lista completa para o explorador de catÃ¡logo da PokÃ©dex
	var accessibleCatalog []map[string]any
	for _, it := range fullQueue {
		accessibleCatalog = append(accessibleCatalog, map[string]any{
			"speciesId": it.SpeciesID,
			"name":      it.Name,
			"slug":      it.Slug,
			"level":     it.Level,
			"types":     it.Types,
			"offensive": it.Offensive,
			"kills":     it.Kills,
			"claimed":   it.Claimed,
			"canClaim":  it.CanClaim,
			"remaining": it.Remaining,
		})
	}

	pct := 0.0
	if totalAccessible > 0 {
		pct = (float64(totalCompleted) * 100.0) / float64(totalAccessible)
	}

	b.mu.Lock()
	slugChanged := b.targetSlug != bestHunt.Slug
	b.targetSlug = bestHunt.Slug
	b.offensiveMult = offMult
	b.incomingRisk = risk
	b.targetTypes = targetTypes
	s := b.socket
	autoHunt := b.config.Settings.AutoHunt && !b.paused
	b.mu.Unlock()

	b.updateStatus(func(st *engine.AccountStatus) {
		st.CurrentArea = bestHunt.Name
		st.Extra["slug"] = bestHunt.Slug
		st.Extra["huntLevel"] = bestHunt.Level
		st.Extra["pokedexKills"] = killsText
		st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
		st.Extra["matchupDetail"] = desc
		st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
		st.Extra["pokedexFarmMode"] = pokedexMode
		st.Extra["pokedexTotalCompleted"] = totalCompleted
		st.Extra["pokedexTotalAccessible"] = totalAccessible
		st.Extra["pokedexPercent"] = fmt.Sprintf("%.1f%%", pct)
		st.Extra["pokedexQueue"] = topQueue
		st.Extra["pokedexCatalog"] = accessibleCatalog
		if activeTarget != nil {
			st.Extra["pokedexCurrentTarget"] = map[string]any{
				"speciesId":   activeTarget.SpeciesID,
				"name":        activeTarget.Name,
				"slug":        activeTarget.Slug,
				"level":       activeTarget.Level,
				"types":       activeTarget.Types,
				"offensive":   activeTarget.Offensive,
				"kills":       activeTarget.Kills,
				"remaining":   activeTarget.Remaining,
				"targetKills": 100,
				"percent":     min(100, activeTarget.Kills),
				"claimed":     activeTarget.Claimed,
			}
		}
	})

	if slugChanged || forceEnter {
		b.addLog(fmt.Sprintf("ðŸŽ¯ [Rota / BestiÃ¡rio] %s [%s] -> %s (Lv. %d) [%s] | Dano: %.1fx | %s | BestiÃ¡rio: %s",
			activeName, strings.Join(activeTypes, "/"), bestHunt.Name, bestHunt.Level,
			strings.Join(targetTypes, "/"), offMult, desc, killsText))
		if s != nil && autoHunt {
			b.sendEnterHunt(false)
		}
	}
}

// sendEnterHunt envia o pacote de entrada na Ã¡rea de caÃ§a atual
func (b *Bot) sendEnterHunt(resume bool) {
	b.mu.RLock()
	s := b.socket
	slug := b.targetSlug
	b.mu.RUnlock()

	if slug == "" || slug == "auto" {
		b.evaluateAndSwitchWeaknessHunt(false)
		b.mu.RLock()
		slug = b.targetSlug
		b.mu.RUnlock()
	}

	if slug == "" || slug == "auto" {
		slug = "geodude"
	}

	if s != nil && s.IsConnected() {
		_ = s.SendJSON(EnterHuntPacket{
			Type:   "enter-hunt",
			Slug:   slug,
			Resume: resume,
		})
	}
}

// buyBalls realiza a compra de PokÃ©bolas na API do jogo
func (b *Bot) buyBalls(ballID, qty int) {
	if b.token == "" {
		return
	}
	req := BuyBallsRequest{BallID: ballID, Qty: qty}
	var res map[string]any
	err := b.httpCli.PostJSON("/api/game/balls/buy", req, &res)
	if err != nil {
		b.addLog(fmt.Sprintf("Erro ao comprar PokÃ©bolas: %v", err))
		return
	}
	b.addLog(fmt.Sprintf("ðŸ›’ Compradas %d PokÃ©bolas (ID %d) com sucesso!", qty, ballID))

	b.mu.RLock()
	s := b.socket
	b.mu.RUnlock()
	if s != nil {
		_ = s.SendJSON(map[string]any{"type": "balls-get"})
	}
}

// checkAutoSell vende pokÃ©mons comuns duplicados, preservando 1 exemplar de cada espÃ©cie para rotaÃ§Ã£o de fraquezas 4.0x
func (b *Bot) checkAutoSell(pokes []PokemonItem) {
	if b.token == "" || len(pokes) == 0 {
		return
	}

	// Identifica espÃ©cies jÃ¡ protegidas ou existentes na equipe
	seenSpecies := make(map[int]bool)
	for _, p := range pokes {
		if p.Team || p.Starter || p.Shiny || p.Locked || p.Listed {
			seenSpecies[p.SpeciesID] = true
		}
	}

	var toSell []any
	for _, p := range pokes {
		// Nunca vende pokÃ©mons da equipe, iniciais, shinies ou trancados
		if p.Team || p.Starter || p.Shiny || p.Locked || p.Listed {
			continue
		}

		if seenSpecies[p.SpeciesID] {
			// JÃ¡ possuÃ­mos um exemplar dessa espÃ©cie! Este Ã© duplicado/repetido, pode vender.
			if p.SellValue > 0 {
				toSell = append(toSell, p.ID)
			}
		} else {
			// Primeiro exemplar desta espÃ©cie: preserva na mochila para rotaÃ§Ãµes 4.0x!
			seenSpecies[p.SpeciesID] = true
			b.addLog(fmt.Sprintf("ðŸ›¡ï¸ [Equipe 4.0x] Novo elemento preservado para rotaÃ§Ã£o: %s (Lv. %d)", p.Name, p.Level))
		}
	}

	if len(toSell) == 0 {
		return
	}

	req := SellPokemonRequest{PokeIDs: toSell}
	var res map[string]any
	err := b.httpCli.PostJSON("/api/game/pokemon/sell", req, &res)
	if err == nil {
		b.addLog(fmt.Sprintf("ðŸ’° Vendidos %d PokÃ©mons duplicados/repetidos.", len(toSell)))
		b.mu.RLock()
		s := b.socket
		b.mu.RUnlock()
		if s != nil {
			_ = s.SendJSON(map[string]any{"type": "pokes-get"})
		}
	}
}

// checkAutoEvolve analisa todos os Pokémons disponíveis e evolui os que atingiram os requisitos
func (b *Bot) checkAutoEvolve(pokes []PokemonItem) {
	b.mu.RLock()
	s := b.socket
	b.mu.RUnlock()
	if s == nil || !s.IsConnected() {
		return
	}

	for _, p := range pokes {
		targetID, canEvolve := b.catalog.GetEvolution(p.SpeciesID, p.Level)
		if canEvolve && targetID > 0 {
			targetName := b.catalog.GetSpeciesName(targetID)
			b.addLog(fmt.Sprintf("🧬 [Evolução] %s (Lv. %d) atingiu os requisitos! Evoluindo para %s (ID %d)...",
				p.Name, p.Level, targetName, targetID))
			_ = s.SendJSON(PokemonEvolvePacket{
				Type:      "pokemon.evoluir",
				PokemonID: p.ID,
				AlvoID:    targetID,
			})
			time.Sleep(1 * time.Second)
		}
	}
}

// runPeriodicFeatures executa automações periódicas de sistemas do jogo (Passe, Ginásio, Oferenda, Chocadeira, Amigos, etc.)
func (b *Bot) runPeriodicFeatures() {
	b.mu.RLock()
	s := b.socket
	cfg := b.config.Settings
	activeTypes := b.activeTypes
	activePoke := b.activePokemon
	b.mu.RUnlock()

	if s == nil || !s.IsConnected() {
		return
	}

	// 1. Passe de Batalha Diário
	if cfg.AutoBattlePass {
		_ = s.SendJSON(BattlePassClaimPacket{Type: "passe.resgatar"})
	}

	// 2. Desafio dos Ginásios Elementais (busca buff de +10% de dano do elemento ativo)
	if cfg.AutoGyms && len(activeTypes) > 0 {
		gymType := activeTypes[0]
		_ = s.SendJSON(GymChallengePacket{Type: "ginasio.desafiar", Tipo: gymType})
	}

	// 3. Altar de Oferendas (converte duplicatas em pedras e essências)
	if cfg.AutoOfferings {
		_ = s.SendJSON(AltarOfferingsAllPacket{Type: "oferenda.tudo"})
	}

	// 4. Chocadeira e Forja de Ovos Shinies
	if cfg.AutoHatchery {
		_ = s.SendJSON(ShinyEggCraftPacket{Type: "ovoShiny.fabricar"})
		_ = s.SendJSON(EggHatchPlacePacket{Type: "chocadeira.colocar", Slot: 0, Ovo: 1})
	}

	// 5. Moedas de Amigos (solicita lista para retribuir moedas)
	if cfg.AutoFriendCoins {
		_ = s.SendJSON(FriendsInfoPacket{Type: "amigos.info"})
	}

	// 6. Refino de Atributos do Titular (aprimora ataque)
	if cfg.AutoRefine && activePoke != "" {
		b.mu.RLock()
		pokes := b.availablePokes
		b.mu.RUnlock()
		for _, p := range pokes {
			if p.Name == activePoke {
				_ = s.SendJSON(PokemonRefinePacket{
					Type:      "pokemon.refinar",
					PokemonID: p.ID,
					Stat:      "atk",
				})
				break
			}
		}
	}
}

// claimDailyRewards reivindica presente diÃ¡rio e recompensas de kill
func (b *Bot) claimDailyRewards() {
	if b.token == "" {
		return
	}
	var dummy map[string]any
	// 1. Presente do calendÃ¡rio diÃ¡rio
	_ = b.httpCli.PostJSON("/api/game/daily", map[string]any{}, &dummy)

	// 2. Recompensa de missÃ£o diÃ¡ria de abate
	_ = b.httpCli.PostJSON("/api/game/daily-kill/claim", map[string]any{}, &dummy)
}

// refreshCharacterInfo consulta os dados atualizados do treinador (nÃ­vel, XP, ouro) na API oficial
func (b *Bot) refreshCharacterInfo() {
	if b.token == "" {
		return
	}
	var charResp CharacterMeResponse
	err := b.httpCli.GetJSON("/api/characters/me", &charResp)
	if err == nil && charResp.Character != nil {
		ch := charResp.Character
		b.mu.Lock()
		oldLevel := b.status.Level
		b.mu.Unlock()

		b.updateStatus(func(s *engine.AccountStatus) {
			s.Name = ch.Name
			s.Level = ch.Level
			if ch.Gold > 0 {
				s.Gold = int64(ch.Gold)
			}
			s.Extra["trainerName"] = ch.Name
			s.Extra["trainerLevel"] = ch.Level
			s.Extra["trainerXP"] = ch.XP
			s.Extra["lookType"] = ch.LookType
		})

		if oldLevel > 0 && ch.Level > oldLevel {
			b.addLog(fmt.Sprintf("ðŸŽ‰ Treinador '%s' avanÃ§ou para o NÃ­vel %d! Re-avaliando rotas de caÃ§a...", ch.Name, ch.Level))
			b.evaluateAndSwitchWeaknessHunt(false)
		}
	}
}

// checkAndClaimPokedex consulta a PokÃ©dex/BestiÃ¡rio, resgata bÃ´nus de +25% XP pendentes e atualiza progresso
func (b *Bot) checkAndClaimPokedex() bool {
	if b.token == "" {
		return false
	}

	// MantÃ©m as informaÃ§Ãµes do treinador (nÃ­vel, gold, XP) sempre sincronizadas com a API
	b.refreshCharacterInfo()

	var res PokedexResponse
	err := b.httpCli.GetJSON("/api/game/pokedex", &res)
	if err != nil {
		return false
	}

	b.mu.Lock()
	if b.pokedexSpecies == nil {
		b.pokedexSpecies = make(map[int]PokedexSpeciesEntry)
	}
	var newlyClaimedSpecies []int

	for _, entry := range res.Species {
		b.pokedexSpecies[entry.ID] = entry
		if entry.CanClaim && b.config.Settings.AutoPokedexClaim {
			newlyClaimedSpecies = append(newlyClaimedSpecies, entry.ID)
		}
	}
	currentSlug := b.targetSlug
	b.mu.Unlock()

	claimedAny := false
	for _, spID := range newlyClaimedSpecies {
		var claimRes PokedexClaimResponse
		req := PokedexClaimRequest{SpeciesID: spID}
		claimErr := b.httpCli.PostJSON("/api/game/pokedex/claim", req, &claimRes)
		if claimErr == nil && claimRes.OK {
			claimedAny = true
			spInfo, _ := b.catalog.GetSpeciesByID(spID)
			spName := spInfo.Name
			if spName == "" {
				spName = fmt.Sprintf("#%d", spID)
			}
			b.addLog(fmt.Sprintf("ðŸ“– [BestiÃ¡rio] ðŸŽ‰ BÃ´nus +25%% XP resgatado para %s (+%d XP)! NÃ­vel: Lv. %d",
				spName, claimRes.XPGained, claimRes.Level))

			b.updateStatus(func(st *engine.AccountStatus) {
				if claimRes.Level > st.Level {
					st.Level = claimRes.Level
				}
			})

			b.mu.Lock()
			if entry, ok := b.pokedexSpecies[spID]; ok {
				entry.Claimed = true
				entry.CanClaim = false
				b.pokedexSpecies[spID] = entry
			}
			b.mu.Unlock()
		}
	}

	// Atualiza progresso da criatura atual no status
	currentSpID := b.catalog.GetSpeciesIDByName(currentSlug)
	b.mu.RLock()
	curEntry, hasCur := b.pokedexSpecies[currentSpID]
	b.mu.RUnlock()

	b.updateStatus(func(st *engine.AccountStatus) {
		if hasCur {
			st.Extra["pokedexKills"] = fmt.Sprintf("%d/100", curEntry.Kills)
			st.Extra["pokedexClaimed"] = curEntry.Claimed
		} else {
			st.Extra["pokedexKills"] = "0/100"
			st.Extra["pokedexClaimed"] = false
		}
	})

	// Se resgatou bÃ´nus ou se o modo BestiÃ¡rio estiver ativo, re-avalia rota e atualiza status
	if b.config.Settings.PokedexFarmMode {
		b.evaluateAndSwitchWeaknessHunt(claimedAny)
	}

	return claimedAny
}

// refreshAccessToken renova o token usando o refreshToken
func (b *Bot) refreshAccessToken() bool {
	if b.refreshToken == "" {
		return false
	}

	var res RefreshTokenResponse
	err := b.httpCli.PostJSON("/api/auth/refresh", map[string]string{"refreshToken": b.refreshToken}, &res)
	if err == nil && res.AccessToken != "" {
		b.token = res.AccessToken
		if res.RefreshToken != "" {
			b.refreshToken = res.RefreshToken
		}
		b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
		log.Printf("[%s] AccessToken renovado com sucesso via refresh!", b.ID())
		return true
	}
	return false
}

// computeWebSocketURL calcula o shard exato do WebSocket usando o hash DJB2 sobre o claim 'sub' do JWT
func (b *Bot) computeWebSocketURL(token string) string {
	baseURL := "wss://poke.idleworld.online/ws"
	shards := 88
	if token == "" {
		return baseURL + "?v=2"
	}

	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return fmt.Sprintf("%s?token=%s&v=2", baseURL, url.QueryEscape(token))
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.StdEncoding.DecodeString(parts[1])
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(payloadBytes, &claims)
	if claims.Sub == "" {
		return fmt.Sprintf("%s?token=%s&v=2", baseURL, url.QueryEscape(token))
	}

	// DJB2 Hash com overflow signed de 32 bits exatamente como o client JS do jogo
	var hash int32 = 5381
	for _, c := range []byte(claims.Sub) {
		hash = (hash << 5) + hash + int32(c)
	}

	val := int(hash)
	if val < 0 {
		val = -val
	}
	shardIdx := val % shards

	targetURL := baseURL
	if shardIdx > 0 {
		targetURL = fmt.Sprintf("wss://poke.idleworld.online/ws%d", shardIdx)
	}

	return fmt.Sprintf("%s?token=%s&v=2", targetURL, url.QueryEscape(token))
}

func (b *Bot) GetStatus() engine.AccountStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

func (b *Bot) ExecuteAction(action string, payload map[string]any) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := b.socket

	switch action {
	case "autoHunt":
		b.config.Settings.AutoHunt = !b.config.Settings.AutoHunt
		if b.config.Settings.AutoHunt {
			b.paused = false
			b.status.Status = "hunting"
			b.appendLogLocked("Auto-hunt ativado.")
			go b.sendEnterHunt(false)
		} else {
			b.paused = true
			b.status.Status = "idle (pausado)"
			b.appendLogLocked("Auto-hunt pausado.")
			if s != nil && s.IsConnected() {
				_ = s.SendJSON(LeaveHuntPacket{Type: "leave-hunt"})
			}
		}
		return map[string]any{"autoHunt": b.config.Settings.AutoHunt}, nil

	case "autoCatch":
		b.config.Settings.AutoCatch = !b.config.Settings.AutoCatch
		b.appendLogLocked(fmt.Sprintf("Auto-catch alternado para: %v", b.config.Settings.AutoCatch))
		return map[string]any{"autoCatch": b.config.Settings.AutoCatch}, nil

	case "joyHeal":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
			b.appendLogLocked("Cura solicitada via Nurse Joy.")
			return "Cura disparada com sucesso!", nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "toggleWeakness":
		b.config.Settings.AutoWeakness = !b.config.Settings.AutoWeakness
		b.status.Extra["autoWeakness"] = b.config.Settings.AutoWeakness
		b.appendLogLocked(fmt.Sprintf("Auto-Fraqueza alternado para: %v", b.config.Settings.AutoWeakness))
		if b.config.Settings.AutoWeakness {
			go b.evaluateAndSwitchWeaknessHunt(true)
		}
		return map[string]any{"autoWeakness": b.config.Settings.AutoWeakness}, nil

	case "setArea":
		area, _ := payload["area"].(string)
		if area == "" {
			area, _ = payload["slug"].(string)
		}
		if area == "" {
			return nil, fmt.Errorf("parÃ¢metro 'area' ou 'slug' obrigatÃ³rio")
		}
		area = strings.ToLower(strings.TrimSpace(area))
		if area == "auto" || area == "fraqueza" || area == "weakness" {
			b.config.Settings.AutoWeakness = true
			b.config.Settings.HuntTarget = "auto"
			b.targetSlug = "auto"
			b.status.Extra["autoWeakness"] = true
			b.appendLogLocked("Modo de CaÃ§a Inteligente por Fraqueza ativado.")
			go b.evaluateAndSwitchWeaknessHunt(true)
			return "Modo Fraqueza AutomÃ¡tica ativado!", nil
		}
		b.config.Settings.AutoWeakness = false
		b.targetSlug = area
		b.config.Settings.HuntTarget = area
		b.status.CurrentArea = b.catalog.GetHuntName(area)
		b.status.Extra["slug"] = area
		b.status.Extra["autoWeakness"] = false
		b.appendLogLocked(fmt.Sprintf("Ãrea alterada para: %s (%s)", b.status.CurrentArea, area))
		go func() {
			b.evaluateAndSwitchWeaknessHunt(false)
			b.sendEnterHunt(false)
		}()
		return fmt.Sprintf("Ãrea alterada para %s", area), nil

	case "buyBalls":
		qty := 50
		if q, ok := payload["qty"].(float64); ok && q > 0 {
			qty = int(q)
		}
		ballID := b.config.Settings.BallID
		if bID, ok := payload["ballId"].(float64); ok && bID > 0 {
			ballID = int(bID)
		}
		go b.buyBalls(ballID, qty)
		return fmt.Sprintf("Comprando %d PokÃ©bolas (ID %d)...", qty, ballID), nil

	case "claimDaily":
		go b.claimDailyRewards()
		b.appendLogLocked("ReivindicaÃ§Ã£o diÃ¡ria disparada.")
		return "Recompensas diÃ¡rias solicitadas!", nil

	case "refresh":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(map[string]any{"type": "pokes-get"})
			_ = s.SendJSON(map[string]any{"type": "balls-get"})
			_ = s.SendJSON(map[string]any{"type": "inv-get"})
			_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
			_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
			return "SincronizaÃ§Ã£o solicitada!", nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "sendChat":
		msg, _ := payload["message"].(string)
		if msg == "" {
			msg, _ = payload["body"].(string)
		}
		if msg == "" {
			return nil, fmt.Errorf("mensagem nÃ£o informada")
		}
		ch, _ := payload["channel"].(string)
		if ch == "" {
			ch = "help"
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(SendChatPacket{Type: "send", Channel: ch, Body: msg})
			b.appendLogLocked(fmt.Sprintf("ðŸ’¬ Mensagem enviada no chat [%s]: %s", ch, msg))
			return "Mensagem enviada no chat!", nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "guildRecruit":
		tName := b.status.Name
		if tName == "" {
			tName = b.Name()
		}
		msg := getRandomRecruitMessage(tName)
		if custom, ok := payload["message"].(string); ok && custom != "" {
			msg = custom
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(SendChatPacket{Type: "send", Channel: "help", Body: msg})
			nextDelay := getRandomRecruitDelay()
			b.lastRecruitPingAt = time.Now()
			b.nextRecruitPingAt = time.Now().Add(nextDelay)
			b.appendLogLocked(fmt.Sprintf("ðŸ“¢ DivulgaÃ§Ã£o de guilda enviada: \"%s\" (prÃ³ximo sorteado em %s)", msg, formatDurationHuman(nextDelay)))
			return fmt.Sprintf("DivulgaÃ§Ã£o enviada no chat! PrÃ³ximo envio sorteado para daqui a %s.", formatDurationHuman(nextDelay)), nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "guildGet":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
			b.appendLogLocked("InformaÃ§Ãµes de guilda solicitadas.")
			return "Status da guilda atualizado!", nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "guildDonate":
		amt := 5000
		if a, ok := payload["amount"].(float64); ok && a > 0 {
			amt = int(a)
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildActionPacket{Type: "guild-action", Action: "donate", Amount: amt})
			b.appendLogLocked(fmt.Sprintf("DoaÃ§Ã£o de %d ouro enviada para a guilda.", amt))
			return fmt.Sprintf("Doado %d de ouro para a guilda!", amt), nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "guildLeave":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildActionPacket{Type: "guild-action", Action: "leave"})
			b.appendLogLocked("SolicitaÃ§Ã£o para sair da guilda enviada.")
			return "SolicitaÃ§Ã£o para sair da guilda enviada!", nil
		}
		return nil, fmt.Errorf("socket nÃ£o conectado")

	case "supplyTicket":
		ticket, _ := payload["ticket"].(string)
		if ticket == "" {
			ticket, _ = payload["token"].(string)
		}
		if ticket == "" {
			return nil, fmt.Errorf("campo ticket/token obrigatÃ³rio")
		}
		b.mu.Unlock()
		err := b.SupplyTicket(ticket, "")
		b.mu.Lock()
		return "Token atualizado com sucesso!", err

	case "claimPokedex", "pokedexClaim":
		go b.checkAndClaimPokedex()
		return "VerificaÃ§Ã£o e resgate da PokÃ©dex disparados!", nil

	case "togglePokedex":
		b.config.Settings.PokedexFarmMode = !b.config.Settings.PokedexFarmMode
		b.status.Extra["pokedexFarmMode"] = b.config.Settings.PokedexFarmMode
		modeStr := "OFF"
		if b.config.Settings.PokedexFarmMode {
			modeStr = "ON"
			go b.evaluateAndSwitchWeaknessHunt(true)
		}
		b.appendLogLocked(fmt.Sprintf("Modo Foco em BestiÃ¡rio alterado para: %s", modeStr))
		return fmt.Sprintf("Modo BestiÃ¡rio alterado para %s!", modeStr), nil

	case "skipBestiaryTarget", "bestiaryNext":
		currentSpID := b.catalog.GetSpeciesIDByName(b.targetSlug)
		if currentSpID > 0 {
			if b.bestiarySkips == nil {
				b.bestiarySkips = make(map[int]bool)
			}
			b.bestiarySkips[currentSpID] = true
			spInfo, _ := b.catalog.GetSpeciesByID(currentSpID)
			name := spInfo.Name
			if name == "" {
				name = b.targetSlug
			}
			b.appendLogLocked(fmt.Sprintf("â© [BestiÃ¡rio] Alvo %s pulado manualmente. Rotacionando para o prÃ³ximo monstro...", name))
			go b.evaluateAndSwitchWeaknessHunt(true)
			return fmt.Sprintf("Alvo %s pulado! Indo para o prÃ³ximo...", name), nil
		}
		return nil, fmt.Errorf("nenhum alvo ativo para pular")

	case "resetBestiarySkips":
		b.bestiarySkips = make(map[int]bool)
		b.appendLogLocked("ðŸ”„ [BestiÃ¡rio] Pulos manuais resetados. Fila normal restabelecida.")
		go b.evaluateAndSwitchWeaknessHunt(true)
		return "Fila do BestiÃ¡rio reiniciada com sucesso!", nil

	default:
		return nil, fmt.Errorf("aÃ§Ã£o desconhecida: %s", action)
	}
}

func (b *Bot) SupplyTicket(ticket, wsURL string) error {
	log.Printf("[%s] Recebido novo token para a conta: %s...", b.ID(), ticket[:min(15, len(ticket))])
	b.saveTokensToAccountsFile(ticket, "")
	select {
	case b.ticketChan <- ticket:
	default:
	}
	return nil
}

func (b *Bot) saveTokensToAccountsFile(token, refreshToken string) {
	content, err := os.ReadFile("accounts.json")
	if err != nil {
		return
	}
	var configs []AccountConfig
	if err := json.Unmarshal(content, &configs); err != nil {
		return
	}
	for i := range configs {
		if configs[i].ID == b.ID() {
			if token != "" {
				configs[i].Credentials.Token = token
			}
			if refreshToken != "" {
				configs[i].Credentials.RefreshToken = refreshToken
			}
			break
		}
	}
	if data, err := json.MarshalIndent(configs, "", "  "); err == nil {
		_ = os.WriteFile("accounts.json", data, 0644)
	}
}

func (b *Bot) updateStatus(fn func(s *engine.AccountStatus)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn(&b.status)
	b.status.UpdatedAt = time.Now().UnixMilli()
}

func (b *Bot) appendLogLocked(msg string) {
	b.status.Logs = append(b.status.Logs, msg)
	if len(b.status.Logs) > 12 {
		b.status.Logs = b.status.Logs[1:]
	}
}

func (b *Bot) addLog(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.appendLogLocked(msg)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
