package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/Arcscribe-Web-Solutions/upmiigo-bridge/pkg/upmiigo"
)

const loginFlowToken = "token"

func (c *Connector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "Bridge token",
		Description: "Paste a token from Up Mii Go: Settings → Beeper bridge → Make a token.",
		ID:          loginFlowToken,
	}}
}

func (c *Connector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != loginFlowToken {
		return nil, fmt.Errorf("unknown login flow %q", flowID)
	}
	return &tokenLogin{main: c, user: user}, nil
}

type tokenLogin struct {
	main *Connector
	user *bridgev2.User
}

var _ bridgev2.LoginProcessUserInput = (*tokenLogin)(nil)

func (t *tokenLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "co.uk.upmiigo.token",
		Instructions: "Make a token on Up Mii Go (Settings → Beeper bridge), then paste it here.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "token",
				Name:        "Bridge token",
				Description: "Starts with umgb_",
				Pattern:     "^umgb_[A-Za-z0-9_-]{20,}$",
			}},
		},
	}, nil
}

func (t *tokenLogin) Cancel() {}

func (t *tokenLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	token := strings.TrimSpace(input["token"])
	api := upmiigo.NewClient(t.main.Config.ServerURL, token)
	me, err := api.Me(ctx)
	if err != nil {
		if upmiigo.IsUnauthorized(err) {
			return nil, fmt.Errorf("that token didn't work: make a new one in Up Mii Go Settings → Beeper bridge")
		}
		return nil, fmt.Errorf("couldn't reach Up Mii Go: %w", err)
	}
	login, err := t.user.NewLogin(ctx, &database.UserLogin{
		ID:         networkid.UserLoginID(me.ID),
		RemoteName: "@" + me.Username,
		RemoteProfile: status.RemoteProfile{
			Username: me.Username,
			Name:     me.Name(),
		},
		Metadata: &UserLoginMetadata{Token: token},
	}, &bridgev2.NewLoginParams{DeleteOnConflict: true})
	if err != nil {
		return nil, fmt.Errorf("couldn't save the login: %w", err)
	}
	go login.Client.Connect(login.Log.WithContext(context.Background()))
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "co.uk.upmiigo.complete",
		Instructions: fmt.Sprintf("Signed in as @%s. Your Up Mii Go conversations will appear shortly.", me.Username),
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}, nil
}
