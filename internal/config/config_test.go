package config

import (
	"io"
	"testing"
	"time"
)

func TestLoadFlagsOverrideEnv(t *testing.T) {
	t.Setenv("FLEETPULSE_SERVER", "env-host:50051")
	t.Setenv("AGENT_TOKEN", "token-env")
	t.Setenv("FLEETPULSE_INTERVAL", "30s")

	cfg, err := Load([]string{"--server", "flag-host:50051", "--interval", "5s"}, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ServerAddr != "flag-host:50051" {
		t.Errorf("ServerAddr = %q, se esperaba el valor del flag", cfg.ServerAddr)
	}
	if cfg.Token != "token-env" {
		t.Errorf("Token = %q, se esperaba el valor del entorno", cfg.Token)
	}
	if cfg.Interval != 5*time.Second {
		t.Errorf("Interval = %s, se esperaba 5s", cfg.Interval)
	}
}

func TestLoadRequiresServerAndToken(t *testing.T) {
	t.Setenv("FLEETPULSE_SERVER", "")
	t.Setenv("AGENT_TOKEN", "")

	if _, err := Load(nil, io.Discard); err == nil {
		t.Fatal("se esperaba error al faltar servidor y token")
	}
}

func TestLoadOnceSkipsServerAndToken(t *testing.T) {
	t.Setenv("FLEETPULSE_SERVER", "")
	t.Setenv("AGENT_TOKEN", "")

	cfg, err := Load([]string{"--once"}, io.Discard)
	if err != nil {
		t.Fatalf("Load con --once: %v", err)
	}
	if !cfg.Once {
		t.Error("Once = false, se esperaba true")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("FLEETPULSE_SERVER", "host:50051")
	t.Setenv("AGENT_TOKEN", "token")

	cases := map[string][]string{
		"modo docker":      {"--docker", "maybe"},
		"nivel de log":     {"--log-level", "trace"},
		"intervalo minimo": {"--interval", "100ms"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(args, io.Discard); err == nil {
				t.Fatalf("se esperaba error para %v", args)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("FLEETPULSE_SERVER", "host:50051")
	t.Setenv("AGENT_TOKEN", "token")

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Interval != DefaultInterval {
		t.Errorf("Interval = %s, se esperaba %s", cfg.Interval, DefaultInterval)
	}
	if cfg.Docker != DockerAuto {
		t.Errorf("Docker = %q, se esperaba auto", cfg.Docker)
	}
	if cfg.StateDir == "" {
		t.Error("StateDir vacio")
	}
}
