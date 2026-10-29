use std::sync::Arc;
use std::time::Duration;
use tokio::sync::RwLock;
use reqwest::{Client, Method};
use anyhow::{anyhow, Result};

use crate::types::*;

const BASE_URL: &str = "https://poke.idleworld.online";
const USER_AGENT: &str = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36";

#[derive(Clone)]
pub struct PokeIdleWorldClient {
    http_client: Client,
    token: Arc<RwLock<String>>,
    refresh_token: Arc<RwLock<String>>,
}

impl PokeIdleWorldClient {
    pub fn new(initial_token: &str, initial_refresh: &str) -> Self {
        let http_client = Client::builder()
            .timeout(Duration::from_secs(15))
            .build()
            .unwrap_or_else(|_| Client::new());

        Self {
            http_client,
            token: Arc::new(RwLock::new(initial_token.to_string())),
            refresh_token: Arc::new(RwLock::new(initial_refresh.to_string())),
        }
    }

    pub async fn set_token(&self, token: &str) {
        let mut t = self.token.write().await;
        *t = token.to_string();
    }

    pub async fn get_token(&self) -> String {
        self.token.read().await.clone()
    }

    pub async fn set_refresh_token(&self, rt: &str) {
        let mut r = self.refresh_token.write().await;
        *r = rt.to_string();
    }

    pub async fn get_refresh_token(&self) -> String {
        self.refresh_token.read().await.clone()
    }

    pub async fn do_request<T: serde::de::DeserializeOwned>(
        &self,
        method: Method,
        endpoint: &str,
        body: Option<serde_json::Value>,
    ) -> Result<T> {
        let url = format!("{}{}", BASE_URL, endpoint);
        let token = self.get_token().await;

        let mut req = self.http_client.request(method, &url)
            .header("Content-Type", "application/json")
            .header("Accept", "application/json, text/plain, */*")
            .header("User-Agent", USER_AGENT);

        if !token.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", token));
        }

        if let Some(b) = body {
            req = req.json(&b);
        }

        let resp = req.send().await?;
        let status = resp.status();
        let resp_bytes = resp.bytes().await?;

        if !status.is_success() {
            let text = String::from_utf8_lossy(&resp_bytes);
            return Err(anyhow!("HTTP {} em {}: {}", status.as_u16(), endpoint, text));
        }

        let parsed: T = serde_json::from_slice(&resp_bytes)
            .map_err(|e| anyhow!("Falha ao parsear {}: {}", endpoint, e))?;

        Ok(parsed)
    }

    pub async fn get_character_me(&self) -> Result<CharacterMeResponse> {
        self.do_request(Method::GET, "/api/characters/me", None).await
    }

    pub async fn refresh_access_token(&self) -> Result<String> {
        let rt = self.get_refresh_token().await;
        if rt.is_empty() {
            return Err(anyhow!("Sem refreshToken"));
        }

        let body = serde_json::json!({ "refreshToken": rt });
        let res: RefreshTokenResponse = self.do_request(Method::POST, "/api/auth/refresh", Some(body)).await?;

        if !res.access_token.is_empty() {
            self.set_token(&res.access_token).await;
            if let Some(ref new_rt) = res.refresh_token {
                self.set_refresh_token(new_rt).await;
            }
            Ok(res.access_token)
        } else {
            Err(anyhow!("Resposta de refresh sem accessToken"))
        }
    }

    pub async fn claim_daily(&self) -> Result<()> {
        let _: serde_json::Value = self.do_request(Method::POST, "/api/daily/claim", Some(serde_json::json!({}))).await?;
        Ok(())
    }
}
