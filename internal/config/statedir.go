package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// defaultStateDir elige donde persistir el agent_id. En Linux se usa la ruta
// estandar para estado de servicios, que es donde escribe la unidad systemd
// que genera install.sh; fuera de root (pruebas locales) y en el resto de
// sistemas se cae al directorio de configuracion del usuario.
func defaultStateDir() string {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		return "/var/lib/fleetpulse"
	}

	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "fleetpulse")
	}
	return filepath.Join(os.TempDir(), "fleetpulse")
}
