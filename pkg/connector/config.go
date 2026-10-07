package connector

import (
	_ "embed"
	"strings"

	up "go.mau.fi/util/configupgrade"
)

//go:embed example-config.yaml
var ExampleConfig string

// Config is the network section of the bridge config.
type Config struct {
	// The up mii go site to bridge.
	ServerURL string `yaml:"server_url"`
	// How many recent messages to bring in per conversation the first time you log in.
	InitialMessages int `yaml:"initial_messages"`
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "server_url")
	helper.Copy(up.Int, "initial_messages")
}

func (c *Config) normalise() {
	c.ServerURL = strings.TrimRight(strings.TrimSpace(c.ServerURL), "/")
	if c.ServerURL == "" {
		c.ServerURL = "https://upmiigo.co.uk"
	}
	if c.InitialMessages <= 0 {
		c.InitialMessages = 30
	}
	if c.InitialMessages > 100 {
		c.InitialMessages = 100
	}
}
