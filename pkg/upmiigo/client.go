// Package upmiigo is a small client for the up mii go bridge API (/api/bridge/v1).
package upmiigo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type User struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	Verified    bool    `json:"verified"`
	Suspended   bool    `json:"suspended,omitempty"`
}

// Name is what to call someone: their display name, or @username.
func (u *User) Name() string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return "@" + u.Username
}

type Conversation struct {
	ID            string     `json:"id"`
	Other         *User      `json:"other"`
	CreatedAt     time.Time  `json:"created_at"`
	LastMessageAt *time.Time `json:"last_message_at"`
	MyReadAt      *time.Time `json:"my_read_at"`
	OtherReadAt   *time.Time `json:"other_read_at"`
}

type Photo struct {
	URL      string `json:"url"`
	Width    *int   `json:"width"`
	Height   *int   `json:"height"`
	MimeType string `json:"mime_type"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
	Photo          *Photo    `json:"photo"`
}

type ReadEvent struct {
	ConversationID string    `json:"conversation_id"`
	UserID         *string   `json:"user_id"`
	ReadAt         time.Time `json:"read_at"`
}

type Events struct {
	Messages      []Message      `json:"messages"`
	Conversations []Conversation `json:"conversations"`
	Reads         []ReadEvent    `json:"reads"`
	Next          string         `json:"next"`
}

// APIError is an error response from the server.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("up mii go: %s (%d): %s", e.Code, e.Status, e.Message)
}

// IsUnauthorized reports whether the token was rejected (revoked or invalid).
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

type Client struct {
	BaseURL string // e.g. https://upmiigo.co.uk
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/bridge/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", "mautrix-upmiigo")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode}
		_ = json.NewDecoder(resp.Body).Decode(apiErr)
		if apiErr.Code == "" {
			apiErr.Code = "http_error"
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) jsonBody(v any) (io.Reader, error) {
	data, err := json.Marshal(v)
	return bytes.NewReader(data), err
}

func (c *Client) Me(ctx context.Context) (*User, error) {
	var me User
	return &me, c.do(ctx, http.MethodGet, "/me", nil, "", &me)
}

func (c *Client) Conversations(ctx context.Context) ([]Conversation, error) {
	var out struct {
		Conversations []Conversation `json:"conversations"`
	}
	return out.Conversations, c.do(ctx, http.MethodGet, "/conversations", nil, "", &out)
}

func (c *Client) Conversation(ctx context.Context, id string) (*Conversation, error) {
	var out struct {
		Conversation Conversation `json:"conversation"`
	}
	return &out.Conversation, c.do(ctx, http.MethodGet, "/conversations/"+url.PathEscape(id), nil, "", &out)
}

// StartConversation opens (or finds) a conversation with a member by username.
func (c *Client) StartConversation(ctx context.Context, username string) (*Conversation, error) {
	body, err := c.jsonBody(map[string]string{"username": username})
	if err != nil {
		return nil, err
	}
	var out struct {
		Conversation Conversation `json:"conversation"`
	}
	return &out.Conversation, c.do(ctx, http.MethodPost, "/conversations", body, "application/json", &out)
}

// Messages returns up to limit messages, oldest first. If before is set, they're the ones just before it.
func (c *Client) Messages(ctx context.Context, conversationID string, before *time.Time, limit int) ([]Message, bool, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != nil {
		q.Set("before", before.UTC().Format(time.RFC3339Nano))
	}
	var out struct {
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
	}
	err := c.do(ctx, http.MethodGet, "/conversations/"+url.PathEscape(conversationID)+"/messages?"+q.Encode(), nil, "", &out)
	return out.Messages, out.HasMore, err
}

func (c *Client) SendText(ctx context.Context, conversationID, text string) (*Message, error) {
	body, err := c.jsonBody(map[string]string{"body": text})
	if err != nil {
		return nil, err
	}
	var out struct {
		Message Message `json:"message"`
	}
	return &out.Message, c.do(ctx, http.MethodPost, "/conversations/"+url.PathEscape(conversationID)+"/messages", body, "application/json", &out)
}

func (c *Client) SendPhoto(ctx context.Context, conversationID string, data []byte, fileName, mimeType, caption string) (*Message, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName)},
		"Content-Type":        {mimeType},
	})
	if err != nil {
		return nil, err
	}
	if _, err = part.Write(data); err != nil {
		return nil, err
	}
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	var out struct {
		Message Message `json:"message"`
	}
	return &out.Message, c.do(ctx, http.MethodPost, "/conversations/"+url.PathEscape(conversationID)+"/photos", &buf, w.FormDataContentType(), &out)
}

func (c *Client) MarkRead(ctx context.Context, conversationID string, upTo time.Time) error {
	body, err := c.jsonBody(map[string]string{"up_to": upTo.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/conversations/"+url.PathEscape(conversationID)+"/read", body, "application/json", nil)
}

func (c *Client) User(ctx context.Context, id string) (*User, error) {
	var out struct {
		User User `json:"user"`
	}
	return &out.User, c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(id), nil, "", &out)
}

// Events long-polls for changes since the given cursor, waiting up to wait for something to happen.
func (c *Client) Events(ctx context.Context, since string, wait time.Duration) (*Events, error) {
	q := url.Values{"wait": {strconv.Itoa(int(wait.Seconds()))}}
	if since != "" {
		q.Set("since", since)
	}
	var out Events
	return &out, c.do(ctx, http.MethodGet, "/events?"+q.Encode(), nil, "", &out)
}

// Download fetches a file (avatar or photo) from a URL the API returned.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	return data, resp.Header.Get("Content-Type"), err
}
