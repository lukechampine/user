package main

import (
	"os"
	"os/user"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

var config struct {
	MuseAddr  string `toml:"muse_addr"`
	SHARDAddr string `toml:"shard_addr"`
	HostSet   string `toml:"host_set"`
	MinShards int    `toml:"min_shards"`
}

func loadConfig() error {
	// TODO: cross-platform location?
	user, err := user.Current()
	if err != nil {
		return err
	}
	defaultDir := filepath.Join(user.HomeDir, ".config", "user")
	_, err = toml.DecodeFile(filepath.Join(defaultDir, "config.toml"), &config)
	if os.IsNotExist(err) {
		// if no config file found, proceed with empty config
		err = nil
	}
	if err != nil {
		return err
	}
	if config.HostSet == "" {
		config.HostSet = "default"
	}
	return nil
}
