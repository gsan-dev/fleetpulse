//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// notifyShutdown construye el contexto de parada para una ejecucion normal
// (systemd en Linux, launchd/terminal en macOS): SIGINT/SIGTERM lo cancelan.
func notifyShutdown() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// isWindowsService siempre es false fuera de Windows: no existe el concepto
// de Service Control Manager.
func isWindowsService() bool { return false }

func runAsWindowsService([]string) error {
	return errors.New("el modo servicio de Windows no esta disponible en este sistema operativo")
}

// runServiceCommand cubre `fleetpulse-agent service ...` en sistemas donde no
// hay SCM: el ciclo de vida del servicio lo gestiona systemd
// (ver install/install.sh y config/system.md, seccion de instalacion).
func runServiceCommand([]string) error {
	return errors.New("la gestion de servicio (install/uninstall/start/stop) es solo de Windows; en Linux instala la unidad systemd con install/install.sh")
}
