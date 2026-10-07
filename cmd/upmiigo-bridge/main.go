// upmiigo-bridge is a Matrix bridge for up mii go direct messages, built on mautrix-go's bridgev2.
// Run it with Beeper's bridge manager (bbctl) to get up mii go chats in Beeper.
package main

import (
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/Arcscribe-Web-Solutions/upmiigo-bridge/pkg/connector"
)

// Set at build time with -ldflags.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "upmiigo-bridge",
		URL:         "https://github.com/Arcscribe-Web-Solutions/upmiigo-bridge",
		Description: "A Matrix bridge for up mii go direct messages.",
		Version:     "0.1.0",
		Connector:   &connector.Connector{},
	}
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
