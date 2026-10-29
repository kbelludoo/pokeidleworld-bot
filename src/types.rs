use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AccountConfig {
    pub id: String,
    pub name: String,
    pub enabled: bool,
    pub credentials: GameCredentials,
    pub settings: GameSettings,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GameCredentials {
    #[serde(default)]
    pub email: String,
    #[serde(default)]
    pub password: String,
    #[serde(default)]
    pub token: String,
    #[serde(default, rename = "refreshToken")]
    pub refresh_token: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GameSettings {
    #[serde(default = "default_hunt_target", rename = "huntTarget")]
    pub hunt_target: String,
    #[serde(default = "default_true", rename = "autoWeakness")]
    pub auto_weakness: bool,
    #[serde(default = "default_true", rename = "autoHunt")]
    pub auto_hunt: bool,
    #[serde(default = "default_true", rename = "autoCatch")]
    pub auto_catch: bool,
    #[serde(default = "default_ball_id", rename = "ballId")]
    pub ball_id: u32,
    #[serde(default = "default_shiny_ball_id", rename = "shinyBallId")]
    pub shiny_ball_id: u32,
    #[serde(default = "default_true", rename = "autoBuyBalls")]
    pub auto_buy_balls: bool,
    #[serde(default = "default_min_balls", rename = "minBalls")]
    pub min_balls: u32,
    #[serde(default = "default_buy_ball_qty", rename = "buyBallQty")]
    pub buy_ball_qty: u32,
    #[serde(default = "default_true", rename = "autoHeal")]
    pub auto_heal: bool,
    #[serde(default = "default_heal_hp", rename = "healThresholdHp")]
    pub heal_threshold_hp: u32,
    #[serde(default = "default_true", rename = "autoDaily")]
    pub auto_daily: bool,
    #[serde(default = "default_true", rename = "autoSell")]
    pub auto_sell: bool,
    #[serde(default = "default_true", rename = "autoGuildAccept")]
    pub auto_guild_accept: bool,
    #[serde(default = "default_true", rename = "autoGuildRecruit")]
    pub auto_guild_recruit: bool,
    #[serde(default = "default_true", rename = "autoPokedexClaim")]
    pub auto_pokedex_claim: bool,
    #[serde(default = "default_true", rename = "pokedexFarmMode")]
    pub pokedex_farm_mode: bool,
}

fn default_hunt_target() -> String { "auto".to_string() }
fn default_true() -> bool { true }
fn default_ball_id() -> u32 { 1 }
fn default_shiny_ball_id() -> u32 { 3 }
fn default_min_balls() -> u32 { 20 }
fn default_buy_ball_qty() -> u32 { 50 }
fn default_heal_hp() -> u32 { 30 }

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CharacterMeResponse {
    pub character: Option<CharacterDto>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CharacterDto {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub level: u32,
    #[serde(default, rename = "lookType")]
    pub look_type: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RefreshTokenResponse {
    #[serde(default, rename = "accessToken")]
    pub access_token: String,
    #[serde(default, rename = "refreshToken")]
    pub refresh_token: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct AccountStatusDto {
    pub id: String,
    pub name: String,
    pub level: u32,
    pub vocation: String,
    pub hp: u32,
    #[serde(rename = "maxHp")]
    pub max_hp: u32,
    pub mana: u32,
    #[serde(rename = "maxMana")]
    pub max_mana: u32,
    pub gold: i64,
    pub kills: u64,
    #[serde(rename = "expPerHour")]
    pub exp_per_hour: f64,
    pub status: String,
    #[serde(rename = "currentArea")]
    pub current_area: String,
    #[serde(rename = "durationSec")]
    pub duration_sec: u64,
    pub logs: Vec<String>,
    #[serde(rename = "updatedAt")]
    pub updated_at: i64,
    pub extra: ExtraStatusDto,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ExtraStatusDto {
    #[serde(rename = "autoWeakness")]
    pub auto_weakness: bool,
    pub balls: u32,
    pub captures: u32,
    pub guild: String,
    #[serde(rename = "guildBonus")]
    pub guild_bonus: String,
    #[serde(rename = "guildTag")]
    pub guild_tag: String,
    pub matchup: String,
    #[serde(rename = "pokedexFarmMode")]
    pub pokedex_farm_mode: bool,
    #[serde(rename = "pokedexKills")]
    pub pokedex_kills: String,
    pub slug: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TelemetryResponse {
    pub accounts: Vec<AccountStatusDto>,
    pub goroutines: u32,
    #[serde(rename = "ramAllocMB")]
    pub ram_alloc_mb: f64,
    #[serde(rename = "ramSysMB")]
    pub ram_sys_mb: f64,
    pub uptime: String,
}
