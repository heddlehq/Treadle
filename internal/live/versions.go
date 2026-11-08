package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Version is a version of a flow: published, kept before a run, or a checkpoint.
type Version struct {
	ID           int            `json:"id"`
	Number       int            `json:"number"`
	Kind         string         `json:"kind"`
	Live         bool           `json:"live"`
	Note         string         `json:"note"`
	Author       *Author        `json:"author"`
	Source       *int           `json:"source_version_number"`
	InsertedAt   time.Time      `json:"inserted_at"`
	Changes      map[string]int `json:"changes"`
	Contributors []Author       `json:"contributors"`
}

type Author struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// Name is how a person reads in a list: their username, or what is before the @.
func (a Author) Name() string {
	if a.Username != "" {
		return a.Username
	}
	for i, r := range a.Email {
		if r == '@' {
			return a.Email[:i]
		}
	}
	return a.Email
}

// Versions lists the versions of a flow, newest first.
func (c *Client) Versions(ctx context.Context, flowID int) ([]Version, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(flowID, "/versions"), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := check(response); err != nil {
		return nil, err
	}

	var body struct {
		Data []Version `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

// Publish makes the draft what production runs, with a note.
func (c *Client) Publish(ctx context.Context, flowID int, note string) (Version, error) {
	var body struct {
		Data Version `json:"data"`
	}
	err := c.post(ctx, flowID, "/publish", map[string]any{"note": note}, &body)
	return body.Data, err
}

// PublishVersion makes an older version what production runs again.
func (c *Client) PublishVersion(ctx context.Context, flowID, versionID int, note string) (Version, error) {
	var body struct {
		Data Version `json:"data"`
	}
	err := c.post(ctx, flowID, fmt.Sprintf("/versions/%d/publish", versionID), map[string]any{"note": note}, &body)
	return body.Data, err
}

// Restore puts a version in the draft, for everyone editing it.
func (c *Client) Restore(ctx context.Context, flowID, versionID int) error {
	return c.post(ctx, flowID, fmt.Sprintf("/versions/%d/restore", versionID), map[string]any{}, nil)
}
