package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// telegramAPIBase permite sustituir el endpoint en tests sin tocar el codigo
// de produccion.
var telegramAPIBase = "https://api.telegram.org"

// Telegram envia alertas a un chat via un bot de Telegram
// (https://core.telegram.org/bots/api#sendmessage).
type Telegram struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegram crea el canal. `botToken` es el token que da @BotFather y
// `chatID` el chat (o canal) donde debe escribir el bot.
func NewTelegram(botToken, chatID string) *Telegram {
	return &Telegram{botToken: botToken, chatID: chatID, client: &http.Client{}}
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Send(ctx context.Context, message string) error {
	body, err := json.Marshal(map[string]string{
		"chat_id": t.chatID,
		"text":    message,
	})
	if err != nil {
		return fmt.Errorf("serializar payload de telegram: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("construir peticion a telegram: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("enviar a telegram: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram respondio %d: %s", resp.StatusCode, payload)
	}
	return nil
}
