package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	AppName            = "Kerebrom"
	BinaryName         = "kerebrom"
	DefaultDBName      = "kerebrom.db"
	DefaultPort        = 7437
	DataDirEnv         = "KEREBROM_DATA_DIR"
	PortEnv            = "KEREBROM_PORT"
	ProjectEnv         = "KEREBROM_PROJECT"
	RemoteTokenEnv     = "KEREBROM_REMOTE_TOKEN"
	DefaultDataDirName = ".kerebrom"
)

type Defaults struct {
	DataDir string
}

func LoadDefaults() Defaults {
	return Defaults{
		DataDir: defaultDataDir(),
	}
}

func (d Defaults) DBPath() string {
	return filepath.Join(d.DataDir, DefaultDBName)
}

func DefaultListenAddr() string {
	if value := os.Getenv(PortEnv); value != "" {
		return fmt.Sprintf("127.0.0.1:%s", value)
	}
	return fmt.Sprintf("127.0.0.1:%d", DefaultPort)
}

func defaultDataDir() string {
	if value := os.Getenv(DataDirEnv); value != "" {
		return value
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultDataDirName
	}

	return filepath.Join(home, DefaultDataDirName)
}
