package tailapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiBase        = "https://api.tailscale.com/api/v2"
	requestTimeout = 30 * time.Second
	maxBody        = 1 << 20

	// ownTailnet is the API's name for the tailnet the token belongs to.
	ownTailnet = "-"

	// A node spends its key within seconds, so it only has to outlive the run.
	keyExpiry = 600
)

// Client writes the tailnet policy tailk3s needs and mints the keys its nodes join with.
type Client struct {
	http  *http.Client
	token string
}

// New returns a client acting for the tailnet an API access token belongs to.
func New(token string) *Client {
	return &Client{http: &http.Client{Timeout: requestTimeout}, token: token}
}

// Grant writes the tags the nodes wear into the policy, with the ssh rule that lets
// tailk3s reach them. Nothing is written when the policy already says all of it.
func (c *Client) Grant(ctx context.Context, a Access) error {
	policy, etag, err := c.policy(ctx)
	if err != nil {
		return err
	}
	wanted, err := merge(policy, a)
	if err != nil {
		return err
	}
	if bytes.Equal(wanted, policy) {
		return nil
	}
	return c.setPolicy(ctx, wanted, etag)
}

// policy reads the tailnet policy file, with the ETag it has to be written back with.
func (c *Client) policy(ctx context.Context) ([]byte, string, error) {
	res, err := c.do(ctx, http.MethodGet, "/acl", nil, "")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", responseError(res, "read the policy")
	}
	body, err := read(res)
	if err != nil {
		return nil, "", err
	}
	return body, res.Header.Get("ETag"), nil
}

// setPolicy writes the policy back, refusing to overwrite a change made since it was
// read.
func (c *Client) setPolicy(ctx context.Context, policy []byte, etag string) error {
	res, err := c.do(ctx, http.MethodPost, "/acl", policy, etag)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusPreconditionFailed {
		return errors.New("write the policy: it changed while tailk3s was reading it, so run again")
	}
	if res.StatusCode != http.StatusOK {
		return responseError(res, "write the policy")
	}
	return nil
}

// MintKey returns a single-use key a node joins the tailnet with, carrying the tag that
// puts it in the policy under its own name.
func (c *Client) MintKey(ctx context.Context, tags []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"capabilities": map[string]any{
			"devices": map[string]any{
				"create": map[string]any{
					"reusable":      false,
					"ephemeral":     false,
					"preauthorized": true,
					"tags":          tags,
				},
			},
		},
		"expirySeconds": keyExpiry,
	})
	if err != nil {
		return "", err
	}

	res, err := c.do(ctx, http.MethodPost, "/keys", body, "")
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", responseError(res, "mint an auth key")
	}

	raw, err := read(res)
	if err != nil {
		return "", err
	}
	var key struct{ Key string }
	if err := json.Unmarshal(raw, &key); err != nil {
		return "", fmt.Errorf("mint an auth key: read the reply: %w", err)
	}
	if key.Key == "" {
		return "", errors.New("mint an auth key: the reply held no key")
	}
	return key.Key, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, etag string) (*http.Response, error) {
	url := apiBase + "/tailnet/" + ownTailnet + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return res, nil
}

func read(res *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// responseError carries the API's own message, since it names the cause.
func responseError(res *http.Response, what string) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxBody))
	return fmt.Errorf("%s: %s: %s", what, res.Status, strings.TrimSpace(string(body)))
}
