package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Discord envia alertas a un canal via un webhook entrante
// (https://discord.com/developers/docs/resources/webhook#execute-webhook).
type Discord struct {
	webhookURL string
	client     *http.Client
}

// NewDiscord crea el canal a partir de la URL del webhook del canal de Discord.
func NewDiscord(webhookURL string) *Discord {
	return &Discord{webhookURL: webhookURL, client: &http.Client{}}
}

func (d *Discord) Name() string { return "discord" }

func (d *Discord) Send(ctx context.Context, message string) error {
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return fmt.Errorf("serializar payload de discord: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("construir peticion a discord: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("enviar a discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord respondio %d: %s", resp.StatusCode, payload)
	}
	return nil
}
