use std::collections::VecDeque;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::sync::{mpsc, RwLock};
use futures_util::{SinkExt, StreamExt};
use tokio_tungstenite::connect_async;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::Message;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use tracing::{info, warn};

use crate::client::PokeIdleWorldClient;
use crate::types::*;

pub struct BotState {
    pub level: u32,
    pub hp: u32,
    pub max_hp: u32,
    pub mana: u32,
    pub max_mana: u32,
    pub gold: i64,
    pub kills: u64,
    pub exp_per_hour: f64,
    pub status: String,
    pub current_area: String,
    pub logs: VecDeque<String>,
    pub start_time: Instant,
    pub current_exp: i64,
    pub is_hunting: bool,
    pub balls: u32,
    pub captures: u32,
    pub guild: String,
    pub guild_tag: String,
    pub guild_bonus: String,
    pub matchup: String,
    pub pokedex_kills: String,
    pub slug: String,
}

impl BotState {
    pub fn new() -> Self {
        Self {
            level: 0,
            hp: 0,
            max_hp: 0,
            mana: 0,
            max_mana: 0,
            gold: 0,
            kills: 0,
            exp_per_hour: 0.0,
            status: "Desconectado".to_string(),
            current_area: "Auto (Fraqueza Inteligente)".to_string(),
            logs: VecDeque::with_capacity(30),
            start_time: Instant::now(),
            current_exp: 0,
            is_hunting: false,
            balls: 0,
            captures: 0,
            guild: "Sem Guilda (Buscando...)".to_string(),
            guild_tag: String::new(),
            guild_bonus: "+0.0% XP".to_string(),
            matchup: "Avaliando...".to_string(),
            pokedex_kills: "Carregando...".to_string(),
            slug: "auto".to_string(),
        }
    }

    pub fn add_log(&mut self, msg: String) {
        let now = chrono_like_now();
        let formatted = format!("[{}] {}", now, msg);
        info!("{}", formatted);
        if self.logs.len() >= 25 {
            self.logs.pop_front();
        }
        self.logs.push_back(formatted);
    }
}

fn chrono_like_now() -> String {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or(Duration::ZERO)
        .as_secs();
    let hours = (now / 3600) % 24;
    let mins = (now / 60) % 60;
    let secs = now % 60;
    format!("{:02}:{:02}:{:02}", hours, mins, secs)
}

#[derive(Clone)]
pub struct PokeIdleWorldBot {
    pub config: AccountConfig,
    pub client: PokeIdleWorldClient,
    pub state: Arc<RwLock<BotState>>,
    pub ws_sender: Arc<RwLock<Option<mpsc::Sender<String>>>>,
}

impl PokeIdleWorldBot {
    pub fn new(config: AccountConfig) -> Self {
        let client = PokeIdleWorldClient::new(&config.credentials.token, &config.credentials.refresh_token);
        Self {
            config,
            client,
            state: Arc::new(RwLock::new(BotState::new())),
            ws_sender: Arc::new(RwLock::new(None)),
        }
    }

    pub async fn add_log(&self, msg: String) {
        let mut st = self.state.write().await;
        st.add_log(msg);
    }

    pub async fn send_ws_message(&self, msg: String) -> bool {
        let guard = self.ws_sender.read().await;
        if let Some(ref tx) = *guard {
            tx.send(msg).await.is_ok()
        } else {
            false
        }
    }

    pub fn compute_websocket_url(&self, token: &str) -> String {
        let base_url = "wss://poke.idleworld.online/ws";
        let shards = 88;
        if token.is_empty() {
            return format!("{}?v=2", base_url);
        }

        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() < 2 {
            return format!("{}?token={}&v=2", base_url, urlencoding::encode(token));
        }

        let claims_sub = if let Ok(decoded) = URL_SAFE_NO_PAD.decode(parts[1]) {
            if let Ok(v) = serde_json::from_slice::<serde_json::Value>(&decoded) {
                v.get("sub").and_then(|s| s.as_str()).unwrap_or("").to_string()
            } else {
                String::new()
            }
        } else {
            String::new()
        };

        if claims_sub.is_empty() {
            return format!("{}?token={}&v=2", base_url, urlencoding::encode(token));
        }

        let mut hash: i32 = 5381;
        for c in claims_sub.bytes() {
            hash = hash.wrapping_shl(5).wrapping_add(hash).wrapping_add(c as i32);
        }

        let val = hash.unsigned_abs() as usize;
        let shard_idx = val % shards;

        let target_url = if shard_idx > 0 {
            format!("wss://poke.idleworld.online/ws{}", shard_idx)
        } else {
            base_url.to_string()
        };

        format!("{}?token={}&v=2", target_url, urlencoding::encode(token))
    }

    pub async fn init(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        self.add_log("Verificando credenciais e token do Poke Idle World...".to_string()).await;

        let token = self.client.get_token().await;
        if !token.is_empty() {
            match self.client.get_character_me().await {
                Ok(char_resp) => {
                    if let Some(ch) = char_resp.character {
                        let mut st = self.state.write().await;
                        st.level = ch.level;
                        st.status = "Token validado".to_string();
                        st.add_log(format!("Autenticado com sucesso como treinador '{}' (Nível {})", ch.name, ch.level));
                        return Ok(());
                    }
                }
                Err(_) => {
                    self.add_log("Token expirado. Tentando renovação via /api/auth/refresh...".to_string()).await;
                    if self.client.refresh_access_token().await.is_ok() {
                        self.add_log("🎉 Token renovado com sucesso via refresh!".to_string()).await;
                        return Ok(());
                    }
                }
            }
        } else {
            let rt = self.client.get_refresh_token().await;
            if !rt.is_empty() {
                self.add_log("Obtendo novo accessToken a partir do refreshToken...".to_string()).await;
                if self.client.refresh_access_token().await.is_ok() {
                    self.add_log("🎉 Token obtido com sucesso via refresh!".to_string()).await;
                    return Ok(());
                }
            }
        }

        self.add_log("Aguardando renovação de token ou ticket de acesso...".to_string()).await;
        Ok(())
    }

    pub async fn run(&self) {
        let bot_clone = self.clone();
        tokio::spawn(async move {
            let mut reconnect_wait = Duration::from_secs(3);
            loop {
                let token = bot_clone.client.get_token().await;
                if token.is_empty() {
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    continue;
                }

                let ws_url = bot_clone.compute_websocket_url(&token);
                {
                    let mut st = bot_clone.state.write().await;
                    st.status = "Conectando ao WebSocket...".to_string();
                }

                let mut req = match ws_url.into_client_request() {
                    Ok(r) => r,
                    Err(e) => {
                        bot_clone.add_log(format!("Erro ao montar requisição WS: {}", e)).await;
                        tokio::time::sleep(reconnect_wait).await;
                        continue;
                    }
                };

                req.headers_mut().insert(
                    "User-Agent",
                    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
                        .parse()
                        .unwrap(),
                );
                req.headers_mut().insert("Origin", "https://poke.idleworld.online".parse().unwrap());

                match connect_async(req).await {
                    Ok((ws_stream, _)) => {
                        bot_clone.add_log("Conectado ao servidor Poke Idle World.".to_string()).await;
                        {
                            let mut st = bot_clone.state.write().await;
                            st.status = "Conectado. Inicializando...".to_string();
                        }
                        reconnect_wait = Duration::from_secs(3);

                        let (mut write, mut read) = ws_stream.split();
                        let (tx, mut rx) = mpsc::channel::<String>(32);

                        *bot_clone.ws_sender.write().await = Some(tx.clone());

                        let write_handle = tokio::spawn(async move {
                            // Envia pacotes iniciais de sincronização
                            let _ = write.send(Message::Text(r#"{"type":"pokes-get"}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"balls-get"}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"autohelper-get"}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"inv-get"}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"analyzer-get"}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"guild-get","with_list":true}"#.to_string())).await;
                            let _ = write.send(Message::Text(r#"{"type":"enter-hunt","slug":"taillow"}"#.to_string())).await;

                            while let Some(msg) = rx.recv().await {
                                if let Err(e) = write.send(Message::Text(msg)).await {
                                    warn!("Falha ao enviar mensagem WS: {}", e);
                                    break;
                                }
                            }
                        });

                        while let Some(msg_res) = read.next().await {
                            match msg_res {
                                Ok(Message::Text(text)) => {
                                    bot_clone.handle_ws_message(&text).await;
                                }
                                Ok(Message::Close(frame)) => {
                                    let reason = frame.as_ref().map(|f| f.to_string()).unwrap_or_default();
                                    bot_clone.add_log(format!("Conexão WebSocket interrompida: websocket: close {}", reason)).await;
                                    if reason.contains("4001") || reason.contains("unauthorized") {
                                        bot_clone.add_log("Token inválido/expirado detectado (4001). Tentando renovar via refreshToken...".to_string()).await;
                                        let _ = bot_clone.client.refresh_access_token().await;
                                    } else if reason.contains("4005") {
                                        reconnect_wait = Duration::from_secs(30);
                                    }
                                    break;
                                }
                                Err(e) => {
                                    bot_clone.add_log(format!("Erro no stream do WebSocket: {}", e)).await;
                                    break;
                                }
                                _ => {}
                            }
                        }

                        *bot_clone.ws_sender.write().await = None;
                        write_handle.abort();
                    }
                    Err(e) => {
                        let err_str = e.to_string();
                        bot_clone.add_log(format!("Conexão WebSocket interrompida: {}", err_str)).await;
                        if err_str.contains("4001") || err_str.contains("unauthorized") {
                            let _ = bot_clone.client.refresh_access_token().await;
                        }
                    }
                }

                {
                    let mut st = bot_clone.state.write().await;
                    st.status = "Desconectado".to_string();
                    st.is_hunting = false;
                }

                tokio::time::sleep(reconnect_wait).await;
            }
        });

        // Loop de Ping (a cada 25s)
        let bot_ping = self.clone();
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_secs(25)).await;
                let _ = bot_ping.send_ws_message(r#"{"type":"ping"}"#.to_string()).await;
            }
        });

        // Loop de Watchdog de Caça (a cada 4s)
        let bot_watchdog = self.clone();
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_secs(4)).await;
                let should_enter = {
                    let st = bot_watchdog.state.read().await;
                    !st.is_hunting && st.status != "Desconectado" && st.status != "Aguardando token..."
                };

                if should_enter {
                    let _ = bot_watchdog.send_ws_message(r#"{"type":"enter-hunt","slug":"taillow"}"#.to_string()).await;
                }
                let _ = bot_watchdog.send_ws_message(r#"{"type":"analyzer-get"}"#.to_string()).await;
            }
        });

        // Loop de Recompensas Diárias (a cada 30 min)
        let bot_daily = self.clone();
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_secs(1800)).await;
                let _ = bot_daily.client.claim_daily().await;
            }
        });
    }

    async fn handle_ws_message(&self, text: &str) {
        let val: serde_json::Value = match serde_json::from_str(text) {
            Ok(v) => v,
            Err(_) => return,
        };

        let msg_type = val.get("type").and_then(|t| t.as_str()).unwrap_or("");

        match msg_type {
            "field-init" => {
                let slug = val.get("slug").and_then(|s| s.as_str()).unwrap_or("taillow");
                let mut st = self.state.write().await;
                st.is_hunting = true;
                st.status = "HUNTING".to_string();
                st.current_area = slug.to_string();
                st.slug = slug.to_string();
            }
            "field-loot" => {
                let gold = val.get("gold").and_then(|g| g.as_i64()).unwrap_or(0);
                let exp = val.get("exp").and_then(|e| e.as_i64()).unwrap_or(0);

                let mut st = self.state.write().await;
                st.gold += gold;
                st.current_exp += exp;
                st.status = "HUNTING".to_string();
            }
            "field-catch" => {
                let mut st = self.state.write().await;
                st.captures += 1;
                st.add_log("🎯 Pokémon capturado no Poke Idle World!".to_string());
            }
            "field-kill" => {
                let mut st = self.state.write().await;
                st.kills += 1;
            }
            "char-stats" | "char-update" => {
                let lvl = val.get("level").and_then(|l| l.as_u64()).unwrap_or(0) as u32;
                let hp = val.get("hp").and_then(|h| h.as_u64()).unwrap_or(0) as u32;
                let max_hp = val.get("maxHp").and_then(|h| h.as_u64()).unwrap_or(0) as u32;

                let mut st = self.state.write().await;
                if lvl > 0 { st.level = lvl; }
                if hp > 0 { st.hp = hp; }
                if max_hp > 0 { st.max_hp = max_hp; }
            }
            "balls-get" => {
                if let Some(total) = val.get("total").and_then(|t| t.as_u64()) {
                    let mut st = self.state.write().await;
                    st.balls = total as u32;
                }
            }
            _ => {}
        }
    }

    pub async fn to_dto(&self) -> AccountStatusDto {
        let st = self.state.read().await;
        let elapsed_sec = st.start_time.elapsed().as_secs();

        AccountStatusDto {
            id: self.config.id.clone(),
            name: self.config.name.clone(),
            level: st.level,
            vocation: "Treinador".to_string(),
            hp: st.hp,
            max_hp: st.max_hp,
            mana: st.mana,
            max_mana: st.max_mana,
            gold: st.gold,
            kills: st.kills,
            exp_per_hour: st.exp_per_hour,
            status: st.status.clone(),
            current_area: st.current_area.clone(),
            duration_sec: elapsed_sec,
            logs: st.logs.iter().cloned().collect(),
            updated_at: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_millis() as i64)
                .unwrap_or(0),
            extra: ExtraStatusDto {
                auto_weakness: self.config.settings.auto_weakness,
                balls: st.balls,
                captures: st.captures,
                guild: st.guild.clone(),
                guild_bonus: st.guild_bonus.clone(),
                guild_tag: st.guild_tag.clone(),
                matchup: st.matchup.clone(),
                pokedex_farm_mode: self.config.settings.pokedex_farm_mode,
                pokedex_kills: st.pokedex_kills.clone(),
                slug: st.slug.clone(),
            },
        }
    }
}
