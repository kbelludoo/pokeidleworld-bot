package game

// AccountConfig modelo de configuração lido do accounts.json
type AccountConfig struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Proxy       string          `json:"proxy,omitempty"`   // HTTP ou SOCKS5 proxy (ex: http://user:pass@host:port)
	LocalIP     string          `json:"localIp,omitempty"` // Bind de IP local na VPS (Multi-IP)
	Credentials GameCredentials `json:"credentials"`
	Settings    GameSettings    `json:"settings"`
}

type GameCredentials struct {
	Email         string `json:"email,omitempty"`
	Password      string `json:"password,omitempty"`
	Token         string `json:"token,omitempty"`         // Access Token JWT (pokeweb:tokens -> accessToken)
	RefreshToken  string `json:"refreshToken,omitempty"`  // Refresh Token JWT (pokeweb:tokens -> refreshToken)
	SessionCookie string `json:"sessionCookie,omitempty"`
}

type GameSettings struct {
	HuntTarget      string `json:"huntTarget"`                // Slug da área de caça (ex: "auto", "geodude", "caterpie")
	AutoWeakness    bool   `json:"autoWeakness,omitempty"`    // Selecionar rota automaticamente com base na fraqueza dos monstros (vantagem de tipo)
	AutoHunt        bool   `json:"autoHunt"`                  // Caçada contínua automática
	AutoCatch       bool   `json:"autoCatch"`                 // Captura automática de monstros derrotados
	BallID          int    `json:"ballId,omitempty"`          // ID da Pokébola para capturas normais (1 = Poke Ball, 2 = Great Ball, 3 = Ultra Ball)
	ShinyBallID     int    `json:"shinyBallId,omitempty"`     // ID da Pokébola para Shinies (5 = Master Ball)
	AutoBuyBalls    bool   `json:"autoBuyBalls,omitempty"`    // Compra automática de Pokébolas quando estoque estiver baixo
	MinBalls        int    `json:"minBalls,omitempty"`        // Limite mínimo de pokébolas antes de recomprar (default: 20)
	BuyBallQty      int    `json:"buyBallQty,omitempty"`      // Quantidade a comprar (default: 50)
	AutoHeal        bool   `json:"autoHeal"`                  // Cura automática via Nurse Joy
	HealThresholdHP int    `json:"healThresholdHp,omitempty"` // % de vida mínima antes de curar (default: 30)
	AutoDaily       bool   `json:"autoDaily"`                 // Reivindicar recompensas diárias (/api/game/daily e missões diárias)
	AutoSell         bool   `json:"autoSell"`                  // Vender pokémons duplicados / comuns sem equipe
	KeepBestIV       bool   `json:"keepBestIv,omitempty"`      // Preservar pokémons com maior IV ao vender
	AutoGuildAccept  bool   `json:"autoGuildAccept,omitempty"`  // Aceitar automaticamente convites de guildas com bônus de XP
	AutoGuildRecruit bool   `json:"autoGuildRecruit,omitempty"` // Divulgar procura de guilda no chat periodicamente
	AutoGuildDonate  bool   `json:"autoGuildDonate,omitempty"`  // Doar ouro excedente para a guilda
}

// ==========================================
// PACIENTES E PAYLOADS DO WEBSOCKET
// ==========================================

// BaseSocketMessage representa qualquer mensagem recebida no WebSocket com campo 'type'
type BaseSocketMessage struct {
	Type string `json:"type"`
}

// Outbound Packets
type ViewPacket struct {
	Type string `json:"type"` // "view"
	HC   int    `json:"hc"`
	HR   int    `json:"hr"`
}

type EnterHuntPacket struct {
	Type   string `json:"type"` // "enter-hunt"
	Slug   string `json:"slug"`
	Resume bool   `json:"resume"`
}

type LeaveHuntPacket struct {
	Type string `json:"type"` // "leave-hunt"
}

type CatchPacket struct {
	Type      string `json:"type"` // "catch"
	PendingID any    `json:"pendingId"`
	BallID    int    `json:"ballId"`
}

type JoyHealPacket struct {
	Type string `json:"type"` // "joy-heal"
}

type SummonPacket struct {
	Type   string `json:"type"` // "poke-summon"
	PokeID any    `json:"pokeId"`
}

type StorePacket struct {
	Type   string `json:"type"` // "poke-store"
	PokeID any    `json:"pokeId"`
}

type UseHealPacket struct {
	Type   string `json:"type"` // "use-heal"
	ItemID int    `json:"itemId"`
}

type SendChatPacket struct {
	Type    string `json:"type"`    // "send"
	Channel string `json:"channel"` // "help", "world", "trade"
	Body    string `json:"body"`
}

type GuildGetPacket struct {
	Type     string `json:"type"`     // "guild-get"
	WithList bool   `json:"withList"` // true para carregar a lista de guildas do servidor
}

type GuildActionPacket struct {
	Type     string `json:"type"`               // "guild-action"
	Action   string `json:"action"`             // "respond", "leave", "donate", "tribute", "invite"
	InviteID any    `json:"inviteId,omitempty"` // ID do convite ao responder
	Accept   bool   `json:"accept,omitempty"`   // true para aceitar convite
	Amount   int    `json:"amount,omitempty"`   // Quantidade para doação de ouro ou tributo
	Name     string `json:"name,omitempty"`     // Nome do jogador ao convidar
}

// Inbound Events

type FieldInitEvent struct {
	Type    string `json:"type"` // "field-init"
	Slug    string `json:"slug"`
	HuntKey string `json:"huntKey"`
	Rows    int    `json:"rows"`
	Cols    int    `json:"cols"`
}

type FieldHero struct {
	HP    int `json:"hp"`
	MaxHP int `json:"maxHp"`
	Level int `json:"level"`
	Row   int `json:"row"`
	Col   int `json:"col"`
}

type FieldMob struct {
	Slot       int    `json:"slot"`
	SpeciesID  int    `json:"speciesId"`
	HP         int    `json:"hp"`
	MaxHP      int    `json:"maxHp"`
	Shiny      bool   `json:"shiny"`
	Dead       bool   `json:"dead"`
	Respawning bool   `json:"respawning"`
	Row        int    `json:"row"`
	Col        int    `json:"col"`
}

type FieldEvent struct {
	Type        string     `json:"type"` // "field"
	Seq         int64      `json:"seq"`
	Hero        FieldHero  `json:"hero"`
	Mobs        []FieldMob `json:"mobs"`
	Fighting    bool       `json:"fighting"`
	AllMobsDead bool       `json:"allMobsDead"`
}

type FieldKillEvent struct {
	Type string `json:"type"` // "field-kill"
	Slot int    `json:"slot,omitempty"`
}

type PendingItem struct {
	ID        any    `json:"id"`
	SpeciesID int    `json:"speciesId"`
	Name      string `json:"name,omitempty"`
	Shiny     bool   `json:"shiny"`
	LookType  int    `json:"looktype"`
}

type PendingEvent struct {
	Type string        `json:"type"` // "pending"
	List []PendingItem `json:"list"`
}

type CatchResultEvent struct {
	Type      string `json:"type"` // "catch-result"
	Success   bool   `json:"success"`
	PendingID any    `json:"pendingId"`
	BallID    int    `json:"ballId"`
}

type CatchCooldownEvent struct {
	Type   string `json:"type"` // "catch-cooldown"
	LeftMs int64  `json:"leftMs"`
}

type HuntCooldownEvent struct {
	Type string `json:"type"` // "hunt-cooldown" ou "fishing-cooldown"
	MS   int64  `json:"ms"`
}

type BallCatalogItem struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	CatchRate float64 `json:"catchRate"`
	Price     int64   `json:"price"`
	Infinite  bool    `json:"infinite"`
}

type BallsEvent struct {
	Type    string            `json:"type"` // "balls"
	Catalog []BallCatalogItem `json:"catalog"`
	Counts  map[string]int    `json:"counts"`
	Gold    int64             `json:"gold"`
}

type PokemonItem struct {
	ID        any     `json:"id"`
	Name      string  `json:"name"`
	SpeciesID int     `json:"speciesId"`
	Level     int     `json:"level"`
	XP        int64   `json:"xp"`
	HP        int     `json:"hp"`
	MaxHP     int     `json:"maxHp"`
	Team      bool    `json:"team"`
	Starter   bool    `json:"starter"`
	Shiny     bool    `json:"shiny"`
	Locked    bool    `json:"locked"`
	Listed    bool    `json:"listed"`
	Quality   float64 `json:"quality"`
	IVTotal   int     `json:"ivTotal"`
	SellValue int64   `json:"sellValue"`
}

type PokesEvent struct {
	Type string        `json:"type"` // "pokes"
	List []PokemonItem `json:"list"`
}

type AnalyzerEvent struct {
	Type          string  `json:"type"` // "analyzer"
	Kills         int     `json:"kills"`
	Seconds       int     `json:"seconds"`
	XPGained      int64   `json:"xpGained"`
	Captures      int     `json:"captures"`
	ShinyCaptures int     `json:"shinyCaptures"`
	CapturesGold  int64   `json:"capturesGold"`
	LootGold      int64   `json:"lootGold"`
	SupplyGold    int64   `json:"supplyGold"`
	LootItems     int     `json:"lootItems"`
	Balance       int64   `json:"balance"`
	Photos        int     `json:"photos"`
	XPPerHour     float64 `json:"xpPerHour,omitempty"`
}

type InventoryItem struct {
	ID       int    `json:"id"`
	Name     string `json:"name,omitempty"`
	Quantity int    `json:"quantity"`
}

type InventoryEvent struct {
	Type  string          `json:"type"` // "inventory"
	Items []InventoryItem `json:"items"`
}

type GuildMember struct {
	CharacterID any    `json:"characterId,omitempty"`
	Name        string `json:"name"`
	Level       int    `json:"level"`
	Rank        int    `json:"rank"` // 0 = Presidente, 1 = Oficial, 2 = Membro
	Online      bool   `json:"online"`
}

type GuildDetails struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Tag         string        `json:"tag"`
	Leader      string        `json:"leader,omitempty"`
	Members     []GuildMember `json:"members,omitempty"`
	MemberCount int           `json:"memberCount,omitempty"`
	Slots       int           `json:"slots"`
	Points      int           `json:"points"`
	Tier        int           `json:"tier"`
	Motd        string        `json:"motd,omitempty"`
	BonusPct    float64       `json:"bonusPct,omitempty"`
}

type GuildInvite struct {
	ID        any    `json:"id"`
	GuildID   any    `json:"guildId,omitempty"`
	GuildName string `json:"guildName,omitempty"`
	GuildTag  string `json:"guildTag,omitempty"`
	InvitedBy string `json:"invitedBy,omitempty"`
}

type GuildListItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Tag      string  `json:"tag"`
	Leader   string  `json:"leader"`
	BonusPct float64 `json:"bonusPct"`
	Members  int     `json:"members"`
	Slots    int     `json:"slots"`
	Points   int     `json:"points"`
	Tier     int     `json:"tier"`
}

type GuildEvent struct {
	Type          string          `json:"type"` // "guild"
	Guild         *GuildDetails   `json:"guild"`
	CanCreate     bool            `json:"canCreate"`
	CreateCost    int64           `json:"createCost"`
	MyGold        int64           `json:"myGold"`
	MyLevel       int             `json:"myLevel"`
	CooldownUntil any             `json:"cooldownUntil"`
	Invites       []GuildInvite   `json:"invites"`
	List          []GuildListItem `json:"list,omitempty"`
}

type ChatMessage struct {
	ID       string `json:"id"`
	Channel  string `json:"channel"`
	FromName string `json:"fromName"`
	Level    int    `json:"level"`
	GuildTag string `json:"guildTag"`
	Body     string `json:"body"`
	At       string `json:"at"`
}

type ChatEvent struct {
	Type string      `json:"type"` // "chat"
	Msg  ChatMessage `json:"msg"`
}

// ==========================================
// REST API STRUCTS
// ==========================================

type CharacterMeResponse struct {
	Character *struct {
		Name       string `json:"name"`
		Level      int    `json:"level"`
		XP         int64  `json:"xp"`
		StarterID  int    `json:"starterId"`
		LookType   int    `json:"lookType"`
		Gender     string `json:"gender"`
		NameLocked bool   `json:"nameLocked"`
	} `json:"character"`
	NeedsEmailVerification bool `json:"needsEmailVerification"`
	MustChangePassword     bool `json:"mustChangePassword"`
}

type RefreshTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

type DailyStatusResponse struct {
	CanClaim     bool `json:"canClaim"`
	ClaimedToday bool `json:"claimedToday"`
}

type DailyKillOption struct {
	Name string `json:"name"`
	Have int    `json:"have"`
	Need int    `json:"need"`
}

type DailyKillResponse struct {
	PickedIdx int               `json:"pickedIdx"`
	Options   []DailyKillOption `json:"options"`
}

type BuyBallsRequest struct {
	BallID int `json:"ballId"`
	Qty    int `json:"qty"`
}

type SellPokemonRequest struct {
	PokeIDs []any `json:"pokeIds"`
}

type SellItemsRequest struct {
	Items []struct {
		ItemID int `json:"itemId"`
		Qty    int `json:"qty"`
	} `json:"items"`
}
