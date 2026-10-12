package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type TokenResult struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

type CDPTarget struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type CDPMessage struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type CDPResponse struct {
	ID     int64 `json:"id"`
	Result struct {
		Result struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"result"`
	} `json:"result"`
	Error any `json:"error,omitempty"`
}

// LoginViaBrowserCDP abre temporariamente o navegador local para autenticar e extrair os tokens de sessão
func LoginViaBrowserCDP(email, password string) (*TokenResult, error) {
	browserPath, err := FindBrowserPath()
	if err != nil {
		return nil, err
	}

	port := 9222
	userDataDir, err := os.MkdirTemp("", "bot_cdp_login_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(userDataDir)

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
		"--window-size=1080,780",
		"https://poke.idleworld.online/login",
	}

	log.Printf("[CDP Login] Iniciando navegador (%s)...", browserPath)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("falha ao iniciar navegador: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Aguardar CDP endpoint ficar pronto
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	var wsURL string

	for i := 0; i < 30; i++ {
		time.Sleep(300 * time.Millisecond)
		resp, err := http.Get(cdpURL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var targets []CDPTarget
			if json.Unmarshal(body, &targets) == nil {
				for _, t := range targets {
					if t.Type == "page" && t.WebSocketDebuggerURL != "" {
						wsURL = t.WebSocketDebuggerURL
						break
					}
				}
				if wsURL != "" {
					break
				}
			}
		}
	}

	if wsURL == "" {
		return nil, fmt.Errorf("timeout aguardando WebSocket do navegador")
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar no CDP: %w", err)
	}
	defer conn.Close()

	log.Println("[CDP Login] Conectado à página de login. Preenchendo credenciais...")

	// 1. Aguarda a página carregar e injeta preenchimento
	time.Sleep(2 * time.Second)

	fillScript := fmt.Sprintf(`(function() {
		let inputs = Array.from(document.querySelectorAll('input'));
		let userInput = inputs.find(i => i.type === 'text' || i.type === 'email' || (i.placeholder && i.placeholder.toLowerCase().includes('email')));
		let passInput = inputs.find(i => i.type === 'password');
		if (userInput) {
			userInput.focus();
			userInput.value = %q;
			userInput.dispatchEvent(new Event('input', { bubbles: true }));
			userInput.dispatchEvent(new Event('change', { bubbles: true }));
		}
		if (passInput) {
			passInput.focus();
			passInput.value = %q;
			passInput.dispatchEvent(new Event('input', { bubbles: true }));
			passInput.dispatchEvent(new Event('change', { bubbles: true }));
		}
		return !!(userInput && passInput);
	})()`, email, password)

	var reqID int64 = 1
	evalCDP(conn, &reqID, fillScript)

	// 2. Loop de verificação de tokens ou clique no submit
	log.Println("[CDP Login] Aguardando resolução do Turnstile / Captcha e confirmação...")

	checkTokensScript := `(function() {
		let raw = sessionStorage.getItem("pokeweb:tokens") || localStorage.getItem("pokeweb:tokens");
		return raw || "";
	})()`

	clickSubmitScript := `(function() {
		let btn = document.querySelector('button[type="submit"], form button');
		if (btn && !btn.disabled) {
			btn.click();
			return true;
		}
		return false;
	})()`

	timeout := time.After(45 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("tempo limite excedido aguardando login")
		case <-ticker.C:
			// Tenta clicar no submit se o botão estiver liberado
			evalCDP(conn, &reqID, clickSubmitScript)

			// Verifica se os tokens já foram salvos
			val := evalCDP(conn, &reqID, checkTokensScript)
			if val != "" && strings.Contains(val, "accessToken") {
				var res TokenResult
				if err := json.Unmarshal([]byte(val), &res); err == nil && res.AccessToken != "" {
					log.Println("[CDP Login] 🎉 Tokens capturados com sucesso via navegador!")
					return &res, nil
				}
			}
		}
	}
}

func evalCDP(conn *websocket.Conn, reqID *int64, expr string) string {
	*reqID++
	id := *reqID
	msg := CDPMessage{
		ID:     id,
		Method: "Runtime.evaluate",
		Params: map[string]any{
			"expression":    expr,
			"returnByValue": true,
		},
	}
	_ = conn.WriteJSON(msg)

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, respData, err := conn.ReadMessage()
		if err != nil {
			return ""
		}
		var resp CDPResponse
		if json.Unmarshal(respData, &resp) == nil && resp.ID == id {
			if strVal, ok := resp.Result.Result.Value.(string); ok {
				return strVal
			}
			return fmt.Sprintf("%v", resp.Result.Result.Value)
		}
	}
}
