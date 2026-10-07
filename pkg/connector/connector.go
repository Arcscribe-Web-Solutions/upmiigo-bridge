// Package connector is the up mii go network connector for mautrix-go's bridgev2.
package connector

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"

	"github.com/rs/zerolog"
	up "go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

// The up mii go logo (the site's favicon, as a 512px PNG), shown as the network icon in Beeper.
//
//go:embed icon.png
var iconPNG []byte

const (
	kvIconMXC  database.Key = "upmiigo_icon_mxc"
	kvIconHash database.Key = "upmiigo_icon_hash"
)

type Connector struct {
	Bridge *bridgev2.Bridge
	Config Config

	// The logo, uploaded to the homeserver (Matrix icons must be mxc:// URIs).
	iconMXC id.ContentURIString
}

var _ bridgev2.NetworkConnector = (*Connector)(nil)

// UserLoginMetadata is stored per login in the bridge database.
type UserLoginMetadata struct {
	Token string `json:"token"`
	// Where the event stream got to (the API's `next` value), so nothing is missed across restarts.
	Cursor string `json:"cursor"`
	// Which version of the network name and icon this login's Beeper space has (see refreshSpace).
	SpaceVersion int `json:"space_version,omitempty"`
	// The up mii go avatar URL that RemoteProfile.Avatar was uploaded from, to spot changes.
	AvatarSource string `json:"avatar_source,omitempty"`
}

// Bump to make every login's personal space pick up a changed name or icon.
const spaceVersion = 1

func (c *Connector) Init(br *bridgev2.Bridge) {
	c.Bridge = br
}

func (c *Connector) Start(ctx context.Context) error {
	c.Config.normalise()
	c.ensureIcon(ctx)
	return nil
}

// ensureIcon uploads the logo once (again only if it changes) and remembers where it went.
// The first time, it's also set as the bridge bot's avatar.
func (c *Connector) ensureIcon(ctx context.Context) {
	sum := sha256.Sum256(iconPNG)
	hash := hex.EncodeToString(sum[:])
	kv := c.Bridge.DB.KV
	if mxc := kv.Get(ctx, kvIconMXC); mxc != "" && kv.Get(ctx, kvIconHash) == hash {
		c.iconMXC = id.ContentURIString(mxc)
		return
	}
	log := zerolog.Ctx(ctx)
	mxc, _, err := c.Bridge.Bot.UploadMedia(ctx, "", iconPNG, "upmiigo.png", "image/png")
	if err != nil {
		log.Warn().Err(err).Msg("Couldn't upload the up mii go logo; the network icon will be blank")
		return
	}
	c.iconMXC = mxc
	kv.Set(ctx, kvIconMXC, string(mxc))
	kv.Set(ctx, kvIconHash, hash)
	if err := c.Bridge.Bot.SetAvatarURL(ctx, mxc); err != nil {
		log.Warn().Err(err).Msg("Couldn't set the bridge bot's avatar")
	}
}

func (c *Connector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Up Mii Go",
		NetworkURL:           "https://upmiigo.co.uk",
		NetworkIcon:          c.iconMXC,
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

// Bumping info makes existing rooms pick up the new name and icon.
func (c *Connector) GetBridgeInfoVersion() (info, capabilities int) {
	return 2, 1
}

func (c *Connector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := login.Metadata.(*UserLoginMetadata)
	login.Client = newClient(c, login, meta.Token)
	return nil
}
