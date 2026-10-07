package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/Arcscribe-Web-Solutions/upmiigo-bridge/pkg/upmiigo"
)

// How long each long-poll waits for something to happen.
const pollWait = 25 * time.Second

type client struct {
	main  *Connector
	login *bridgev2.UserLogin
	api   *upmiigo.Client

	mu       sync.Mutex
	stop     context.CancelFunc
	loggedIn bool
}

var (
	_ bridgev2.NetworkAPI                    = (*client)(nil)
	_ bridgev2.ReadReceiptHandlingNetworkAPI = (*client)(nil)
)

func newClient(main *Connector, login *bridgev2.UserLogin, token string) *client {
	main.Config.normalise()
	return &client{main: main, login: login, api: upmiigo.NewClient(main.Config.ServerURL, token)}
}

func (c *client) meID() string { return string(c.login.ID) }

func (c *client) portalKey(conversationID string) networkid.PortalKey {
	return networkid.PortalKey{ID: networkid.PortalID(conversationID), Receiver: c.login.ID}
}

// Connect checks the token, then starts following new messages in the background.
func (c *client) Connect(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	c.login.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting})
	me, err := c.api.Me(ctx)
	if err == nil {
		c.refreshProfile(ctx, me)
	}
	if err != nil {
		if upmiigo.IsUnauthorized(err) {
			c.login.BridgeState.Send(status.BridgeState{
				StateEvent: status.StateBadCredentials,
				Error:      "upmiigo-token-revoked",
				Message:    "Your Up Mii Go token was revoked. Make a new one in Settings → Beeper bridge and log in again.",
			})
			return
		}
		log.Err(err).Msg("Couldn't reach up mii go")
		c.login.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: "upmiigo-unreachable", Message: err.Error()})
		// Retry in the background.
	}
	c.mu.Lock()
	if c.stop != nil {
		c.stop()
	}
	loopCtx, cancel := context.WithCancel(log.WithContext(context.Background()))
	c.stop = cancel
	c.loggedIn = true
	c.mu.Unlock()
	c.refreshSpace(ctx)
	go c.run(loopCtx)
}

// refreshProfile keeps the account's name and picture (shown on the account in Beeper) in step with up mii go.
func (c *client) refreshProfile(ctx context.Context, me *upmiigo.User) {
	profile := c.login.RemoteProfile
	changed := profile.Name != me.Name() || profile.Username != me.Username
	profile.Name, profile.Username = me.Name(), me.Username
	avatarKey := ""
	if me.AvatarURL != nil {
		avatarKey = *me.AvatarURL
	}
	meta := c.login.Metadata.(*UserLoginMetadata)
	if avatarKey != meta.AvatarSource {
		profile.Avatar = ""
		if avatarKey != "" {
			if data, mimeType, err := c.api.Download(ctx, avatarKey); err == nil {
				if mxc, _, err := c.main.Bridge.Bot.UploadMedia(ctx, "", data, "avatar"+extFor(mimeType), mimeType); err == nil {
					profile.Avatar = mxc
				} else {
					zerolog.Ctx(ctx).Warn().Err(err).Msg("Couldn't upload your profile picture")
				}
			}
		}
		meta.AvatarSource = avatarKey
		changed = true
	}
	if changed {
		c.login.RemoteProfile = profile
		c.login.RemoteName = "@" + me.Username
		if err := c.login.Save(ctx); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Couldn't save the login profile")
		}
	}
}

// refreshSpace updates the name and icon of this login's personal space (the sidebar group in Beeper).
// bridgev2 only sets them when the space is first created, so spaces made by an older version keep a
// stale name and no icon. This runs once per spaceVersion.
func (c *client) refreshSpace(ctx context.Context) {
	meta := c.login.Metadata.(*UserLoginMetadata)
	if c.login.SpaceRoom == "" || meta.SpaceVersion >= spaceVersion || c.main.iconMXC == "" {
		return
	}
	log := zerolog.Ctx(ctx)
	bot := c.main.Bridge.Bot
	name := c.main.GetName().DisplayName
	states := []struct {
		evtType event.Type
		content any
	}{
		{event.StateRoomName, &event.RoomNameEventContent{Name: fmt.Sprintf("%s (%s)", name, c.login.RemoteName)}},
		{event.StateTopic, &event.TopicEventContent{Topic: fmt.Sprintf("Your %s bridged chats - %s", name, c.login.RemoteName)}},
		{event.StateRoomAvatar, &event.RoomAvatarEventContent{URL: c.main.iconMXC}},
	}
	for _, st := range states {
		if _, err := bot.SendState(ctx, c.login.SpaceRoom, st.evtType, "", &event.Content{Parsed: st.content}, time.Now()); err != nil {
			log.Warn().Err(err).Str("event_type", st.evtType.Type).Msg("Couldn't update the personal space")
			return
		}
	}
	meta.SpaceVersion = spaceVersion
	if err := c.login.Save(ctx); err != nil {
		log.Warn().Err(err).Msg("Couldn't save the login after updating the space")
	}
	log.Info().Msg("Updated the personal space's name and icon")
}

func (c *client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		c.stop()
		c.stop = nil
	}
}

func (c *client) IsLoggedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggedIn
}

// LogoutRemote stops bridging. The token itself is revoked from the up mii go website.
func (c *client) LogoutRemote(ctx context.Context) {
	c.Disconnect()
	c.mu.Lock()
	c.loggedIn = false
	c.mu.Unlock()
}

func (c *client) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return string(userID) == c.meID()
}

// run does the first sync if needed, then long-polls for events until stopped.
func (c *client) run(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	meta := c.login.Metadata.(*UserLoginMetadata)
	backoff := time.Second

	if meta.Cursor == "" {
		cursor, err := c.initialSync(ctx)
		if err != nil {
			log.Err(err).Msg("Initial sync failed, will retry")
		} else {
			meta.Cursor = cursor
			_ = c.login.Save(ctx)
		}
	}

	connected := false
	lastSave := time.Now()
	for ctx.Err() == nil {
		events, err := c.api.Events(ctx, meta.Cursor, pollWait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if upmiigo.IsUnauthorized(err) {
				c.login.BridgeState.Send(status.BridgeState{
					StateEvent: status.StateBadCredentials,
					Error:      "upmiigo-token-revoked",
					Message:    "Your Up Mii Go token was revoked. Make a new one in Settings → Beeper bridge and log in again.",
				})
				return
			}
			log.Warn().Err(err).Dur("retry_in", backoff).Msg("Event poll failed")
			c.login.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: "upmiigo-unreachable", Message: err.Error()})
			connected = false
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 2*time.Minute)
			continue
		}
		backoff = time.Second
		if !connected {
			c.login.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
			connected = true
		}
		if meta.Cursor == "" {
			// The first sync failed earlier: try it again now the server is reachable.
			if cursor, err := c.initialSync(ctx); err == nil {
				meta.Cursor = cursor
				_ = c.login.Save(ctx)
				continue
			}
		}
		for i := range events.Messages {
			c.queueMessage(&events.Messages[i])
		}
		for _, r := range events.Reads {
			c.queueReceipt(r)
		}
		for _, conv := range events.Conversations {
			c.queueResync(conv.ID)
		}
		if events.Next != "" && events.Next != meta.Cursor {
			meta.Cursor = events.Next
			// Saving on every poll is wasteful; every minute (or after activity) is plenty.
			if len(events.Messages) > 0 || time.Since(lastSave) > time.Minute {
				_ = c.login.Save(ctx)
				lastSave = time.Now()
			}
		}
	}
}

// initialSync brings in each conversation with its recent messages, and returns the cursor to follow from.
func (c *client) initialSync(ctx context.Context) (string, error) {
	cursor := time.Now().UTC().Format(time.RFC3339Nano)
	convs, err := c.api.Conversations(ctx)
	if err != nil {
		return "", err
	}
	for _, conv := range convs {
		if conv.LastMessageAt == nil {
			c.queueResync(conv.ID)
			continue
		}
		msgs, _, err := c.api.Messages(ctx, conv.ID, nil, c.main.Config.InitialMessages)
		if err != nil {
			return "", fmt.Errorf("fetching messages for %s: %w", conv.ID, err)
		}
		for i := range msgs {
			c.queueMessage(&msgs[i])
		}
		if conv.OtherReadAt != nil && conv.Other != nil {
			c.queueReceipt(upmiigo.ReadEvent{ConversationID: conv.ID, UserID: &conv.Other.ID, ReadAt: *conv.OtherReadAt})
		}
	}
	return cursor, nil
}

func (c *client) sender(userID string) bridgev2.EventSender {
	if userID == c.meID() {
		return bridgev2.EventSender{IsFromMe: true, SenderLogin: c.login.ID, Sender: networkid.UserID(userID)}
	}
	return bridgev2.EventSender{Sender: networkid.UserID(userID)}
}

func (c *client) queueMessage(msg *upmiigo.Message) {
	c.login.QueueRemoteEvent(&simplevent.Message[*upmiigo.Message]{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventMessage,
			PortalKey:    c.portalKey(msg.ConversationID),
			Sender:       c.sender(msg.SenderID),
			CreatePortal: true,
			Timestamp:    msg.CreatedAt,
		},
		Data:               msg,
		ID:                 networkid.MessageID(msg.ID),
		ConvertMessageFunc: c.convertMessage,
	})
}

func (c *client) queueReceipt(r upmiigo.ReadEvent) {
	if r.UserID == nil || *r.UserID == c.meID() {
		return
	}
	c.login.QueueRemoteEvent(&simplevent.Receipt{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventReadReceipt,
			PortalKey: c.portalKey(r.ConversationID),
			Sender:    c.sender(*r.UserID),
			Timestamp: r.ReadAt,
		},
		ReadUpTo: r.ReadAt,
	})
}

func (c *client) queueResync(conversationID string) {
	c.login.QueueRemoteEvent(&simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    c.portalKey(conversationID),
			CreatePortal: true,
		},
	})
}

// convertMessage turns an up mii go message into Matrix: text, or a photo re-uploaded to Matrix with any caption.
func (c *client) convertMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg *upmiigo.Message) (*bridgev2.ConvertedMessage, error) {
	if msg.Photo == nil {
		return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: msg.Body},
		}}}, nil
	}
	data, mimeType, err := c.api.Download(ctx, msg.Photo.URL)
	if err != nil {
		// Don't lose the message over a failed download: say what it was.
		zerolog.Ctx(ctx).Warn().Err(err).Str("message_id", msg.ID).Msg("Couldn't download photo")
		body := "[Photo, open Up Mii Go to see it]"
		if msg.Body != "" {
			body += "\n" + msg.Body
		}
		return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: body},
		}}}, nil
	}
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = msg.Photo.MimeType
	}
	fileName := "photo" + extFor(mimeType)
	content := &event.MessageEventContent{
		MsgType:  event.MsgImage,
		Body:     fileName,
		FileName: fileName,
		Info:     &event.FileInfo{MimeType: mimeType, Size: len(data)},
	}
	if msg.Photo.Width != nil && msg.Photo.Height != nil {
		content.Info.Width, content.Info.Height = *msg.Photo.Width, *msg.Photo.Height
	}
	if msg.Body != "" {
		content.Body = msg.Body // a caption, since FileName is set
	}
	content.URL, content.File, err = intent.UploadMedia(ctx, portal.MXID, data, fileName, mimeType)
	if err != nil {
		return nil, fmt.Errorf("uploading photo to Matrix: %w", err)
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content}}}, nil
}

func extFor(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

// HandleMatrixMessage sends a message typed in Beeper to up mii go.
func (c *client) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	conversationID := string(msg.Portal.ID)
	var sent *upmiigo.Message
	var err error
	switch msg.Content.MsgType {
	case event.MsgText, event.MsgNotice:
		sent, err = c.api.SendText(ctx, conversationID, msg.Content.Body)
	case event.MsgEmote:
		sent, err = c.api.SendText(ctx, conversationID, "* "+msg.Content.Body)
	case event.MsgImage:
		uri := msg.Content.URL
		if msg.Content.File != nil {
			uri = msg.Content.File.URL
		}
		data, derr := c.main.Bridge.Bot.DownloadMedia(ctx, uri, msg.Content.File)
		if derr != nil {
			return nil, fmt.Errorf("downloading image from Matrix: %w", derr)
		}
		mimeType := "image/jpeg"
		if msg.Content.Info != nil && msg.Content.Info.MimeType != "" {
			mimeType = msg.Content.Info.MimeType
		}
		caption := ""
		if msg.Content.FileName != "" && msg.Content.Body != msg.Content.FileName {
			caption = msg.Content.Body
		}
		name := msg.Content.FileName
		if name == "" {
			name = "photo" + extFor(mimeType)
		}
		sent, err = c.api.SendPhoto(ctx, conversationID, data, name, mimeType, caption)
	default:
		return nil, bridgev2.ErrUnsupportedMessageType
	}
	if err != nil {
		var apiErr *upmiigo.APIError
		if errors.As(err, &apiErr) {
			return nil, fmt.Errorf("Up Mii Go refused it: %s", apiErr.Message)
		}
		return nil, err
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        networkid.MessageID(sent.ID),
			SenderID:  networkid.UserID(c.meID()),
			Timestamp: sent.CreatedAt,
		},
	}, nil
}

// HandleMatrixReadReceipt marks the conversation read on up mii go (shows as "Seen" there).
func (c *client) HandleMatrixReadReceipt(ctx context.Context, receipt *bridgev2.MatrixReadReceipt) error {
	upTo := receipt.ReadUpTo
	if upTo.IsZero() {
		upTo = time.Now()
	}
	return c.api.MarkRead(ctx, string(receipt.Portal.ID), upTo)
}

func (c *client) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	conv, err := c.api.Conversation(ctx, string(portal.ID))
	if err != nil {
		return nil, err
	}
	roomType := database.RoomTypeDM
	members := &bridgev2.ChatMemberList{
		IsFull: true,
		MemberMap: bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
			EventSender: c.sender(c.meID()),
			Membership:  event.MembershipJoin,
		}),
	}
	if conv.Other != nil {
		members.OtherUserID = networkid.UserID(conv.Other.ID)
		members.MemberMap.Set(bridgev2.ChatMember{
			EventSender: c.sender(conv.Other.ID),
			Membership:  event.MembershipJoin,
			UserInfo:    c.userInfo(conv.Other),
		})
	}
	return &bridgev2.ChatInfo{Type: &roomType, Members: members}, nil
}

func (c *client) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	user, err := c.api.User(ctx, string(ghost.ID))
	if err != nil {
		return nil, err
	}
	return c.userInfo(user), nil
}

func (c *client) userInfo(u *upmiigo.User) *bridgev2.UserInfo {
	name := u.Name()
	if u.Verified {
		name += " ✓"
	}
	info := &bridgev2.UserInfo{
		Name:        &name,
		Identifiers: []string{"upmiigo:" + u.Username},
	}
	if u.AvatarURL != nil && *u.AvatarURL != "" {
		avatarURL := *u.AvatarURL
		info.Avatar = &bridgev2.Avatar{
			ID: networkid.AvatarID(avatarURL),
			Get: func(ctx context.Context) ([]byte, error) {
				data, _, err := c.api.Download(ctx, avatarURL)
				return data, err
			},
		}
	} else {
		info.Avatar = &bridgev2.Avatar{Remove: true}
	}
	return info
}

func (c *client) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:            "co.uk.upmiigo.capabilities.2026_10",
		MaxTextLength: 2000,
		ReadReceipts:  true,
		File: event.FileFeatureMap{
			event.MsgImage: {
				MimeTypes: map[string]event.CapabilitySupportLevel{
					"image/jpeg": event.CapLevelFullySupported,
					"image/png":  event.CapLevelFullySupported,
					"image/webp": event.CapLevelFullySupported,
				},
				Caption:          event.CapLevelFullySupported,
				MaxCaptionLength: 2000,
				MaxSize:          10 << 20,
			},
		},
	}
}
