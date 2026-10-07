// Package connector is the up mii go network connector for mautrix-go's bridgev2.
package connector

import (
	"context"

	up "go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

type Connector struct {
	Bridge *bridgev2.Bridge
	Config Config
}

var _ bridgev2.NetworkConnector = (*Connector)(nil)

// UserLoginMetadata is stored per login in the bridge database.
type UserLoginMetadata struct {
	Token string `json:"token"`
	// Where the event stream got to (the API's `next` value), so nothing is missed across restarts.
	Cursor string `json:"cursor"`
}

func (c *Connector) Init(br *bridgev2.Bridge) {
	c.Bridge = br
}

func (c *Connector) Start(ctx context.Context) error {
	c.Config.normalise()
	return nil
}

func (c *Connector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "up mii go",
		NetworkURL:           "https://upmiigo.co.uk",
		NetworkID:            "upmiigo",
		BeeperBridgeType:     "github.com/Arcscribe-Web-Solutions/upmiigo-bridge",
		DefaultPort:          29399,
		DefaultCommandPrefix: "!umg",
	}
}

func (c *Connector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

func (c *Connector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (c *Connector) GetConfig() (example string, data any, upgrader up.Upgrader) {
	return ExampleConfig, &c.Config, up.SimpleUpgrader(upgradeConfig)
}

func (c *Connector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

func (c *Connector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := login.Metadata.(*UserLoginMetadata)
	login.Client = newClient(c, login, meta.Token)
	return nil
}
