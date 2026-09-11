package alert

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTelegramSendConstruyeLaPeticionCorrecta(t *testing.T) {
	var gotPath string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	original := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = original }()

	tg := NewTelegram("bot-token", "chat-42")
	if err := tg.Send(context.Background(), "hola"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotPath != "/botbot-token/sendMessage" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["chat_id"] != "chat-42" || gotBody["text"] != "hola" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestTelegramSendPropagaErroresHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("bot bloqueado"))
	}))
	defer server.Close()

	original := telegramAPIBase
	telegramAPIBase = server.URL
	defer func() { telegramAPIBase = original }()

	tg := NewTelegram("bot-token", "chat-42")
	err := tg.Send(context.Background(), "hola")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, se esperaba que mencionase 403", err)
	}
}

func TestDiscordSendEnviaElContenido(t *testing.T) {
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	d := NewDiscord(server.URL)
	if err := d.Send(context.Background(), "nodo caido"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody["content"] != "nodo caido" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestMultiIgnoraCanalesQueFallan(t *testing.T) {
	var okCalls int

	failing := failingChannel{}
	ok := recordingChannel{onSend: func() { okCalls++ }}

	alerter := New(noopLogger(), failing, ok)
	alerter.NotifyUnreachable(context.Background(), "a1", "web-01", time.Now())

	if okCalls != 1 {
		t.Errorf("okCalls = %d, se esperaba 1 (el canal que falla no debe bloquear a los demas)", okCalls)
	}
}

func TestNewSinCanalesDevuelveNoop(t *testing.T) {
	alerter := New(noopLogger())
	if _, ok := alerter.(Noop); !ok {
		t.Errorf("alerter = %T, se esperaba Noop cuando no hay canales", alerter)
	}
}

type failingChannel struct{}

func (failingChannel) Name() string { return "failing" }
func (failingChannel) Send(context.Context, string) error {
	return context.DeadlineExceeded
}

type recordingChannel struct{ onSend func() }

func (recordingChannel) Name() string { return "recording" }
func (c recordingChannel) Send(context.Context, string) error {
	c.onSend()
	return nil
}
