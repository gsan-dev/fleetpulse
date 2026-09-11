package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentIDPersisteEntreArranques(t *testing.T) {
	dir := t.TempDir()

	first, err := loadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("primera llamada: %v", err)
	}
	if len(first) != 32 {
		t.Errorf("agent_id = %q, se esperaban 32 caracteres hex", first)
	}

	second, err := loadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("segunda llamada: %v", err)
	}
	if first != second {
		t.Errorf("el agent_id cambio entre arranques: %q -> %q", first, second)
	}

	if _, err := os.Stat(filepath.Join(dir, agentIDFile)); err != nil {
		t.Errorf("no se persistio el fichero de estado: %v", err)
	}
}

func TestAgentIDIgnoraFicheroVacio(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, agentIDFile)
	if err := os.WriteFile(path, []byte("  \n"), 0o640); err != nil {
		t.Fatal(err)
	}

	id, err := loadOrCreateAgentID(dir)
	if err != nil {
		t.Fatalf("loadOrCreateAgentID: %v", err)
	}
	if id == "" {
		t.Error("se esperaba un agent_id nuevo al encontrar el fichero vacio")
	}
}

func TestLoadRellenaHuellaDelNodo(t *testing.T) {
	node, err := Load(context.Background(), t.TempDir(), "v0.1.0-test", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if node.AgentID == "" {
		t.Error("AgentID vacio")
	}
	if node.Hostname == "" {
		t.Error("Hostname vacio")
	}
	if node.OS == "" || node.Arch == "" {
		t.Errorf("OS/Arch vacios: %q %q", node.OS, node.Arch)
	}
	if node.AgentVersion != "v0.1.0-test" {
		t.Errorf("AgentVersion = %q", node.AgentVersion)
	}
	if node.MemoryTotal == 0 {
		t.Error("MemoryTotal = 0")
	}
}

func TestLoadRespetaElOverrideDeHostname(t *testing.T) {
	// El caso real que motiva esto: el agente corre dentro de un contenedor
	// Docker, donde os.Hostname() devuelve el ID del contenedor en vez del
	// nombre de la maquina que lo aloja.
	node, err := Load(context.Background(), t.TempDir(), "v0.1.0-test", "servidor-real")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if node.Hostname != "servidor-real" {
		t.Errorf("Hostname = %q, se esperaba el override \"servidor-real\"", node.Hostname)
	}
}
