//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// windowsServiceName es el nombre interno registrado en el Service Control
// Manager; install.ps1 usa el mismo nombre para gestionarlo desde PowerShell.
const windowsServiceName = "FleetPulseAgent"

// notifyShutdown cubre la ejecucion interactiva (una consola con
// `fleetpulse-agent`, sin pasar por el SCM): Ctrl+C la cancela igual que en
// Linux/macOS. La ejecucion como servicio real usa windowsService.Execute,
// que gobierna su propio contexto.
func notifyShutdown() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// isWindowsService detecta si el proceso lo arranco el Service Control
// Manager (en vez de un usuario desde una consola).
func isWindowsService() bool {
	is, err := svc.IsWindowsService()
	return err == nil && is
}

// windowsService adapta runWithContext al bucle de eventos que exige el SCM:
// Execute se bloquea hasta que el SCM pide parar, momento en el que cancela
// el contexto que runWithContext esta usando para todo (gRPC, Docker, ticker).
type windowsService struct {
	args []string
}

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- runWithContext(ctx, s.args) }()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				changes <- svc.Status{State: svc.Stopped}
				return true, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0

		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-errCh // espera a que runWithContext termine de cerrar todo
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

func runAsWindowsService(args []string) error {
	return svc.Run(windowsServiceName, &windowsService{args: args})
}

// runServiceCommand implementa `fleetpulse-agent service install|uninstall|start|stop`,
// la contrapartida Windows de instalar la unidad systemd en Linux.
func runServiceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: fleetpulse-agent service <install|uninstall|start|stop> [flags-del-agente...]")
	}

	switch args[0] {
	case "install":
		return installService(args[1:])
	case "uninstall":
		return uninstallService()
	case "start":
		return controlService(func(s *mgr.Service) error { return s.Start() })
	case "stop":
		return controlService(func(s *mgr.Service) error { _, err := s.Control(svc.Stop); return err })
	default:
		return fmt.Errorf("subcomando desconocido: %s (usa install, uninstall, start o stop)", args[0])
	}
}

func installService(agentArgs []string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolver la ruta del ejecutable: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("conectar con el Service Control Manager (¿PowerShell como Administrador?): %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(windowsServiceName); err == nil {
		existing.Close()
		return fmt.Errorf("el servicio %s ya esta instalado (usa 'service uninstall' primero)", windowsServiceName)
	}

	s, err := m.CreateService(windowsServiceName, exePath, mgr.Config{
		DisplayName:      "FleetPulse Agent",
		Description:      "Agente de telemetria de FleetPulse: metricas de sistema y contenedores.",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
	}, agentArgs...)
	if err != nil {
		return fmt.Errorf("crear el servicio: %w", err)
	}
	defer s.Close()

	return nil
}

func uninstallService() error {
	return controlService(func(s *mgr.Service) error { return s.Delete() })
}

func controlService(action func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("conectar con el Service Control Manager (¿PowerShell como Administrador?): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(windowsServiceName)
	if err != nil {
		return fmt.Errorf("el servicio %s no esta instalado: %w", windowsServiceName, err)
	}
	defer s.Close()

	return action(s)
}
