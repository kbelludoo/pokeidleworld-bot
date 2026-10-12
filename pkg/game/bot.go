package game

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

type Bot struct {
	config  AccountConfig
	mu      sync.RWMutex
	status  engine.AccountStatus
	socket  *network.SocketClient
	httpCli *network.HTTPClient
	catalog *Catalog

	// Sessão e autenticação
	token         string
	refreshToken  string
	targetSlug    string
	paused        bool
	isHunting     bool
	capturesCount int
	ballCounts    map[string]int

	// Fraqueza e vantagem de tipos
	activePokemon string
	activeTypes   []string
	activeLevel   int
	targetTypes   []string
	offensiveMult float64
	incomingRisk  float64

	// Guilda e Recrutamento
	guild             *GuildDetails
	invites           []GuildInvite
	lastGuildCheckAt  time.Time
	lastRecruitPingAt time.Time

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

	// Calibrar padrões inteligentes
	if cfg.Settings.BallID <= 0 {
		cfg.Settings.BallID = 1 // Poke Ball
	}
	if cfg.Settings.ShinyBallID <= 0 || cfg.Settings.ShinyBallID == 5 {
		cfg.Settings.ShinyBallID = 3 // Ultra Ball (Master Ball ID 5 é bloqueada pelo servidor em Shinies)
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
	// Habilitar auto-guild por padrão se não definido
	if !cfg.Settings.AutoGuildAccept {
		cfg.Settings.AutoGuildAccept = true
	}
	if !cfg.Settings.AutoGuildRecruit {
		cfg.Settings.AutoGuildRecruit = true
	}

	st := engine.NewStatus(cfg.ID, cfg.Name)
	if cfg.Settings.AutoWeakness || cfg.Settings.HuntTarget == "auto" {
		st.CurrentArea = "Auto (Fraqueza Inteligente)"
	} else {
		st.CurrentArea = cat.GetHuntName(cfg.Settings.HuntTarget)
	}
	st.Extra["slug"] = cfg.Settings.HuntTarget
	st.Extra["autoWeakness"] = cfg.Settings.AutoWeakness
	st.Extra["matchup"] = "Avaliando..."
	st.Extra["captures"] = 0
	st.Extra["balls"] = 0
	st.Extra["guild"] = "Sem Guilda (Buscando...)"
	st.Extra["guildTag"] = ""
	st.Extra["guildBonus"] = "+0.0% XP"

	httpCli := network.NewHTTPClientWithNetwork("https://poke.idleworld.online", 15*time.Second, cfg.Proxy, cfg.LocalIP)

	return &Bot{
		config:       cfg,
		status:       st,
		httpCli:      httpCli,
		catalog:      cat,
		token:        cfg.Credentials.Token,
		refreshToken: cfg.Credentials.RefreshToken,
		targetSlug:   cfg.Settings.HuntTarget,
		ballCounts:   make(map[string]int),
		ticketChan:   make(chan string, 1),
		disconnectCh: make(chan error, 1),
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
			log.Printf("[%s] Autenticado com sucesso como treinador '%s' (Nível %d)", b.ID(), ch.Name, ch.Level)
			return nil
		}

		// Se o token falhou e temos refreshToken, tenta renovar
		if b.refreshToken != "" {
			log.Printf("[%s] Token expirado. Tentando renovação via /api/auth/refresh...", b.ID())
			if b.refreshAccessToken() {
				return nil
			}
		}
	}

	// 2. Se o usuário forneceu refreshToken diretamente
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

		log.Printf("[%s] Login automático via CDP não completou (%v).", b.ID(), err)
		log.Printf("[%s] 💡 AVISO: O login web do Poke Idle World exige Turnstile Anti-Robô.", b.ID())
		log.Printf("[%s] 👉 Para conectar imediatamente, pegue o accessToken no navegador (DevTools > SessionStorage > pokeweb:tokens) e cole em 'credentials.token' no accounts.json ou envie via Dashboard: http://localhost:3001/api/ticket", b.ID())
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

// Run executa a conexão com o WebSocket e os loops de jogo
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
				log.Printf("[%s] Token recebido via SupplyTicket. Iniciando conexão...", b.ID())
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

		// Envia pacotes iniciais de sincronização
		_ = socket.SendJSON(map[string]any{"type": "pokes-get"})
		_ = socket.SendJSON(map[string]any{"type": "balls-get"})
		_ = socket.SendJSON(map[string]any{"type": "autohelper-get"})
		_ = socket.SendJSON(map[string]any{"type": "inv-get"})
		_ = socket.SendJSON(map[string]any{"type": "analyzer-get"})
		_ = socket.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})

		// Entra na área de caça se AutoHunt estiver habilitado e rota já estiver definida
		b.mu.RLock()
		currentSlug := b.targetSlug
		b.mu.RUnlock()
		if b.config.Settings.AutoHunt && !b.paused && currentSlug != "" && currentSlug != "auto" {
			b.sendEnterHunt(false)
		}

		// Loop de supervisão interna da conexão
		runErr := b.superviseSession(ctx)
		socket.Close()

		if runErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}

		log.Printf("[%s] Conexão encerrada. Reconectando em 2 segundos...", b.ID())
		time.Sleep(2 * time.Second)
	}
}

// superviseSession monitora keepalive, watchdog de caça e tarefas periódicas
func (b *Bot) superviseSession(ctx context.Context) error {
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	watchdogTicker := time.NewTicker(4 * time.Second)
	defer watchdogTicker.Stop()

	guildTicker := time.NewTicker(30 * time.Second)
	defer guildTicker.Stop()

	dailyTicker := time.NewTicker(30 * time.Minute)
	defer dailyTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-b.disconnectCh:
			b.updateStatus(func(s *engine.AccountStatus) {
				s.Status = "Desconectado"
			})
			b.addLog(fmt.Sprintf("Conexão WebSocket interrompida: %v", err))
			return err

		case newTicket := <-b.ticketChan:
			b.token = newTicket
			b.httpCli.SetHeader("Authorization", "Bearer "+b.token)
			b.addLog("Novo token aplicado. Reiniciando sessão...")
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
				// Se estiver caçando e sem atualização do campo por mais de 12 segundos, re-envia enter-hunt
				if hunting && time.Since(lastField) > 12*time.Second {
					b.sendEnterHunt(true)
				}
				// Solicita atualização periódica do analyzer a cada tick do watchdog
				_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
			}

		case <-guildTicker.C:
			b.mu.RLock()
			s := b.socket
			currentGuild := b.guild
			autoRecruit := b.config.Settings.AutoGuildRecruit
			lastPing := b.lastRecruitPingAt
			tName := b.status.Name
			if tName == "" {
				tName = b.Name()
			}
			b.mu.RUnlock()

			if s != nil && s.IsConnected() {
				// 1. Atualiza dados e lista da guilda a cada 30 segundos
				_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})

				// 2. Se estiver sem guilda e autoRecruit estiver ativo, envia mensagem no canal help a cada 15 min
				if currentGuild == nil && autoRecruit && (lastPing.IsZero() || time.Since(lastPing) >= 15*time.Minute) {
					b.mu.Lock()
					b.lastRecruitPingAt = time.Now()
					b.mu.Unlock()

					msg := fmt.Sprintf("Procuro guild ativa com bônus de XP, nick: %s (farmando 24/7)", tName)
					_ = s.SendJSON(SendChatPacket{
						Type:    "send",
						Channel: "help",
						Body:    msg,
					})
					b.addLog("📢 [Guilda] Divulgação de recrutamento enviada no canal help do jogo!")
				}
			}

		case <-dailyTicker.C:
			if b.config.Settings.AutoDaily {
				go b.claimDailyRewards()
			}
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
				// Responde com o campo de visão exigido pelo servidor
				_ = s.SendJSON(ViewPacket{Type: "view", HC: 15, HR: 9})
			}

			areaName := b.catalog.GetHuntName(ev.Slug)
			desc := FormatMatchupDescription(offMult, risk)
			b.updateStatus(func(st *engine.AccountStatus) {
				st.Status = "hunting"
				st.CurrentArea = areaName
				st.Extra["slug"] = ev.Slug
				st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
				st.Extra["matchupDetail"] = desc
				st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
			})
			b.addLog(fmt.Sprintf("Entrou na rota de caça: %s (%s) | %s", areaName, ev.Slug, desc))
		}

	case "field":
		var ev FieldEvent
		if err := json.Unmarshal(data, &ev); err == nil {
			b.mu.Lock()
			b.lastFieldAt = time.Now()
			b.isHunting = true
			oldLevel := b.status.Level
			b.mu.Unlock()

			b.updateStatus(func(st *engine.AccountStatus) {
				st.Status = "hunting"
				if ev.Hero.MaxHP > 0 {
					st.HP = ev.Hero.HP
					st.MaxHP = ev.Hero.MaxHP
				}
				if ev.Hero.Level > 0 {
					st.Level = ev.Hero.Level
				}
			})

			// Se o jogador subiu de nível, re-avalia alvos de fraqueza para caçar mobs de nível maior
			if oldLevel > 0 && ev.Hero.Level > oldLevel && b.config.Settings.AutoWeakness {
				b.addLog(fmt.Sprintf("🎉 Nível alcançado: Lv. %d! Re-avaliando rotas com fraqueza...", ev.Hero.Level))
				b.evaluateAndSwitchWeaknessHunt(false)
			}

			// Verificação de vida baixa para cura automática
			if ev.Hero.MaxHP > 0 && b.config.Settings.AutoHeal {
				hpPct := (ev.Hero.HP * 100) / ev.Hero.MaxHP
				if hpPct <= b.config.Settings.HealThresholdHP {
					b.mu.RLock()
					s := b.socket
					b.mu.RUnlock()
					if s != nil {
						_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
						b.addLog(fmt.Sprintf("Vida crítica (%d%%). Nurse Joy curou a equipe!", hpPct))
					}
				}
			}
		}

	case "field-kill":
		b.updateStatus(func(st *engine.AccountStatus) {
			st.Kills++
		})
		b.mu.RLock()
		s := b.socket
		b.mu.RUnlock()
		if s != nil {
			_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
		}

	case "field-lost":
		b.addLog("A equipe foi derrotada em batalha. Curando com Nurse Joy e reiniciando caça...")
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
				st.Status = "idle (fora de caça)"
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
								chosenBall = 3 // Fallback para Ultra Ball (Master Ball é bloqueada para Shinies)
							}
						}
						_ = s.SendJSON(CatchPacket{
							Type:      "catch",
							PendingID: item.ID,
							BallID:    chosenBall,
						})
						b.addLog(fmt.Sprintf("Pokébola (ID %d) lançada em monstro pendente #%v (Shiny: %v)", chosenBall, item.SpeciesID, item.Shiny))
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
				b.addLog(fmt.Sprintf("✨ Captura bem-sucedida com Pokébola ID %d! Total: %d", ev.BallID, b.capturesCount))
			} else {
				b.addLog(fmt.Sprintf("Pokébola ID %d falhou na captura.", ev.BallID))
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

			// Auto compra de Pokébolas se estiver abaixo do mínimo
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

				// Avalia fraquezas com base no Pokémon da equipe
				b.evaluateAndSwitchWeaknessHunt(targetSlug == "auto" || targetSlug == "")
			}

			if !hasTeam && len(ev.List) > 0 && s != nil {
				// Invoca o primeiro Pokémon disponível para a equipe
				p := ev.List[0]
				types := b.catalog.GetPokemonTypes(p.SpeciesID, p.Name)
				b.mu.Lock()
				b.activePokemon = p.Name
				b.activeLevel = p.Level
				b.activeTypes = types
				b.mu.Unlock()

				_ = s.SendJSON(SummonPacket{Type: "poke-summon", PokeID: p.ID})
				b.addLog(fmt.Sprintf("Invocando Pokémon %s (Lv. %d) para a equipe...", p.Name, p.Level))
				time.Sleep(500 * time.Millisecond)
				_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
				time.Sleep(500 * time.Millisecond)

				b.evaluateAndSwitchWeaknessHunt(true)
			} else if teamNeedsHeal && s != nil && b.config.Settings.AutoHeal {
				_ = s.SendJSON(JoyHealPacket{Type: "joy-heal"})
				b.addLog("Pokémon da equipe sem vida. Curado com Nurse Joy!")
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
						b.addLog(fmt.Sprintf("💰 [Guilda] Doado %d de ouro para a guilda %s [%s]!", donateAmt, g.Name, g.Tag))
					}
				}
			} else {
				b.updateStatus(func(st *engine.AccountStatus) {
					st.Extra["guild"] = "Sem Guilda (Aguardando convite)"
					st.Extra["guildName"] = "Nenhuma"
					st.Extra["guildTag"] = ""
					st.Extra["guildBonus"] = "+0.0% XP"
				})

				// Se recebemos convites e autoAccept está ativo
				if len(ev.Invites) > 0 && autoAccept && s != nil {
					bestInvite := ev.Invites[0]
					b.addLog(fmt.Sprintf("📩 [Guilda] Recebido convite da guilda %s [%s] (convidado por: %s)!",
						bestInvite.GuildName, bestInvite.GuildTag, bestInvite.InvitedBy))

					// Envia aceitação imediata com o pacote oficial comprovado
					_ = s.SendJSON(GuildActionPacket{
						Type:     "guild-action",
						Action:   "respond",
						InviteID: bestInvite.ID,
						Accept:   true,
					})
					b.addLog(fmt.Sprintf("🎉 [Guilda] Convite aceito! Entramos na guilda %s [%s]!",
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
			// Se alguém nos mencionou ou convidou no chat
			if strings.Contains(bodyLower, tName) || strings.Contains(bodyLower, "kbelludoo") {
				b.addLog(fmt.Sprintf("💬 [Chat %s] %s: %s", ev.Msg.Channel, ev.Msg.FromName, ev.Msg.Body))
				// Imediatamente atualiza guild-get para pegar o convite
				b.mu.RLock()
				s := b.socket
				b.mu.RUnlock()
				if s != nil {
					_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
				}
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

// evaluateAndSwitchWeaknessHunt analisa os tipos do Pokémon ativo e escolhe a rota onde o inimigo tem maior fraqueza
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
		// Modo manual: calcula eficácia do alvo atual para exibição
		targetTypes := b.catalog.GetHuntTypes(currentSlug)
		offMult := GetBestOffensiveMultiplier(activeTypes, targetTypes)
		risk := GetIncomingRisk(activeTypes, targetTypes)
		desc := FormatMatchupDescription(offMult, risk)

		b.mu.Lock()
		b.offensiveMult = offMult
		b.incomingRisk = risk
		b.targetTypes = targetTypes
		b.mu.Unlock()

		b.updateStatus(func(st *engine.AccountStatus) {
			st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
			st.Extra["matchupDetail"] = desc
			st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
		})
		return
	}

	// Modo Auto-Fraqueza: busca rota ideal no catálogo com base nas fraquezas elementais
	bestHunt, offMult, risk, found := b.catalog.FindBestWeaknessHunt(activeTypes, playerLevel)
	if !found {
		return
	}

	targetTypes := b.catalog.GetHuntTypes(bestHunt.Slug)
	desc := FormatMatchupDescription(offMult, risk)

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
		st.Extra["matchup"] = fmt.Sprintf("%.1fx Dano", offMult)
		st.Extra["matchupDetail"] = desc
		st.Extra["enemyTypes"] = strings.Join(targetTypes, " / ")
	})

	if slugChanged || forceEnter {
		b.addLog(fmt.Sprintf("🎯 [Fraqueza Detectada] %s [%s] -> %s (Lv. %d) [%s] | Dano: %.1fx | %s",
			activeName, strings.Join(activeTypes, "/"), bestHunt.Name, bestHunt.Level,
			strings.Join(targetTypes, "/"), offMult, desc))
		if s != nil && autoHunt {
			b.sendEnterHunt(false)
		}
	}
}

// sendEnterHunt envia o pacote de entrada na área de caça atual
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

// buyBalls realiza a compra de Pokébolas na API do jogo
func (b *Bot) buyBalls(ballID, qty int) {
	if b.token == "" {
		return
	}
	req := BuyBallsRequest{BallID: ballID, Qty: qty}
	var res map[string]any
	err := b.httpCli.PostJSON("/api/game/balls/buy", req, &res)
	if err != nil {
		b.addLog(fmt.Sprintf("Erro ao comprar Pokébolas: %v", err))
		return
	}
	b.addLog(fmt.Sprintf("🛒 Compradas %d Pokébolas (ID %d) com sucesso!", qty, ballID))

	b.mu.RLock()
	s := b.socket
	b.mu.RUnlock()
	if s != nil {
		_ = s.SendJSON(map[string]any{"type": "balls-get"})
	}
}

// checkAutoSell vende pokémons comuns duplicados que não estejam na equipe
func (b *Bot) checkAutoSell(pokes []PokemonItem) {
	if b.token == "" || len(pokes) == 0 {
		return
	}

	var toSell []any
	for _, p := range pokes {
		// Nunca vende pokémons da equipe, iniciais, shinies ou trancados
		if p.Team || p.Starter || p.Shiny || p.Locked || p.Listed {
			continue
		}
		if p.SellValue > 0 {
			toSell = append(toSell, p.ID)
		}
	}

	if len(toSell) == 0 {
		return
	}

	req := SellPokemonRequest{PokeIDs: toSell}
	var res map[string]any
	err := b.httpCli.PostJSON("/api/game/pokemon/sell", req, &res)
	if err == nil {
		b.addLog(fmt.Sprintf("💰 Vendidos %d Pokémons duplicados/comuns.", len(toSell)))
		b.mu.RLock()
		s := b.socket
		b.mu.RUnlock()
		if s != nil {
			_ = s.SendJSON(map[string]any{"type": "pokes-get"})
		}
	}
}

// claimDailyRewards reivindica presente diário e recompensas de kill
func (b *Bot) claimDailyRewards() {
	if b.token == "" {
		return
	}
	var dummy map[string]any
	// 1. Presente do calendário diário
	_ = b.httpCli.PostJSON("/api/game/daily", map[string]any{}, &dummy)

	// 2. Recompensa de missão diária de abate
	_ = b.httpCli.PostJSON("/api/game/daily-kill/claim", map[string]any{}, &dummy)
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
		return nil, fmt.Errorf("socket não conectado")

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
			return nil, fmt.Errorf("parâmetro 'area' ou 'slug' obrigatório")
		}
		area = strings.ToLower(strings.TrimSpace(area))
		if area == "auto" || area == "fraqueza" || area == "weakness" {
			b.config.Settings.AutoWeakness = true
			b.config.Settings.HuntTarget = "auto"
			b.targetSlug = "auto"
			b.status.Extra["autoWeakness"] = true
			b.appendLogLocked("Modo de Caça Inteligente por Fraqueza ativado.")
			go b.evaluateAndSwitchWeaknessHunt(true)
			return "Modo Fraqueza Automática ativado!", nil
		}
		b.config.Settings.AutoWeakness = false
		b.targetSlug = area
		b.config.Settings.HuntTarget = area
		b.status.CurrentArea = b.catalog.GetHuntName(area)
		b.status.Extra["slug"] = area
		b.status.Extra["autoWeakness"] = false
		b.appendLogLocked(fmt.Sprintf("Área alterada para: %s (%s)", b.status.CurrentArea, area))
		go func() {
			b.evaluateAndSwitchWeaknessHunt(false)
			b.sendEnterHunt(false)
		}()
		return fmt.Sprintf("Área alterada para %s", area), nil

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
		return fmt.Sprintf("Comprando %d Pokébolas (ID %d)...", qty, ballID), nil

	case "claimDaily":
		go b.claimDailyRewards()
		b.appendLogLocked("Reivindicação diária disparada.")
		return "Recompensas diárias solicitadas!", nil

	case "refresh":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(map[string]any{"type": "pokes-get"})
			_ = s.SendJSON(map[string]any{"type": "balls-get"})
			_ = s.SendJSON(map[string]any{"type": "inv-get"})
			_ = s.SendJSON(map[string]any{"type": "analyzer-get"})
			_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
			return "Sincronização solicitada!", nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "sendChat":
		msg, _ := payload["message"].(string)
		if msg == "" {
			msg, _ = payload["body"].(string)
		}
		if msg == "" {
			return nil, fmt.Errorf("mensagem não informada")
		}
		ch, _ := payload["channel"].(string)
		if ch == "" {
			ch = "help"
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(SendChatPacket{Type: "send", Channel: ch, Body: msg})
			b.appendLogLocked(fmt.Sprintf("💬 Mensagem enviada no chat [%s]: %s", ch, msg))
			return "Mensagem enviada no chat!", nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "guildRecruit":
		tName := b.status.Name
		if tName == "" {
			tName = b.Name()
		}
		msg := fmt.Sprintf("Procuro guild ativa com bônus de XP, nick: %s (farmando 24/7)", tName)
		if custom, ok := payload["message"].(string); ok && custom != "" {
			msg = custom
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(SendChatPacket{Type: "send", Channel: "help", Body: msg})
			b.lastRecruitPingAt = time.Now()
			b.appendLogLocked(fmt.Sprintf("📢 Divulgação de guilda enviada: %s", msg))
			return "Divulgação de recrutamento enviada no chat help!", nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "guildGet":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildGetPacket{Type: "guild-get", WithList: true})
			b.appendLogLocked("Informações de guilda solicitadas.")
			return "Status da guilda atualizado!", nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "guildDonate":
		amt := 5000
		if a, ok := payload["amount"].(float64); ok && a > 0 {
			amt = int(a)
		}
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildActionPacket{Type: "guild-action", Action: "donate", Amount: amt})
			b.appendLogLocked(fmt.Sprintf("Doação de %d ouro enviada para a guilda.", amt))
			return fmt.Sprintf("Doado %d de ouro para a guilda!", amt), nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "guildLeave":
		if s != nil && s.IsConnected() {
			_ = s.SendJSON(GuildActionPacket{Type: "guild-action", Action: "leave"})
			b.appendLogLocked("Solicitação para sair da guilda enviada.")
			return "Solicitação para sair da guilda enviada!", nil
		}
		return nil, fmt.Errorf("socket não conectado")

	case "supplyTicket":
		ticket, _ := payload["ticket"].(string)
		if ticket == "" {
			ticket, _ = payload["token"].(string)
		}
		if ticket == "" {
			return nil, fmt.Errorf("campo ticket/token obrigatório")
		}
		b.mu.Unlock()
		err := b.SupplyTicket(ticket, "")
		b.mu.Lock()
		return "Token atualizado com sucesso!", err

	default:
		return nil, fmt.Errorf("ação desconhecida: %s", action)
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
