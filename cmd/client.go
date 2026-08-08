package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	envAPIURL      = "SUMI_API_URL"
	envAPIKey      = "SUMI_API_KEY"
	envAccessToken = "SUMI_ACCESS_TOKEN"
	defaultAPIURL  = "http://localhost:3000"
	requestTimeout = 15 * time.Second
)

// apiError carries the server's own message so the CLI never invents its own
// wording for a server-side rejection.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Message)
}

// apiClient talks to the API with exactly one credential: an API key for the data
// commands, or a bearer token for the account commands under `sumi auth`.
type apiClient struct {
	baseURL string
	apiKey  string
	bearer  string
	http    *http.Client

	// Set for bearer clients so an expired access token can be refreshed and the
	// refreshed pair written back to disk.
	cfg     *localConfig
	cfgPath string
}

func resolveBaseURL(cfg *localConfig) string {
	if fromEnv := strings.TrimSpace(os.Getenv(envAPIURL)); fromEnv != "" {
		return strings.TrimRight(fromEnv, "/")
	}
	if cfg != nil && strings.TrimSpace(cfg.APIURL) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/")
	}
	return defaultAPIURL
}

// newAPIClient builds the API-key client used by the data commands. The
// environment wins over the stored config so a container can inject credentials.
func newAPIClient() (*apiClient, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(os.Getenv(envAPIKey))
	if apiKey == "" {
		apiKey = strings.TrimSpace(cfg.APIKey)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key: set %s, or run `sumi auth login` then `sumi auth key create`", envAPIKey)
	}

	return &apiClient{
		baseURL: resolveBaseURL(cfg),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: requestTimeout},
	}, nil
}

// newAuthClient builds the bearer client used by `sumi auth`; API keys cannot
// manage sessions or other keys.
func newAuthClient() (*apiClient, error) {
	cfg, path, err := loadConfig()
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(os.Getenv(envAccessToken))
	if token == "" {
		token = strings.TrimSpace(cfg.AccessToken)
	}
	if token == "" {
		return nil, fmt.Errorf("not logged in: run `sumi auth login --email <email> --password-stdin`")
	}

	return &apiClient{
		baseURL: resolveBaseURL(cfg),
		bearer:  token,
		http:    &http.Client{Timeout: requestTimeout},
		cfg:     cfg,
		cfgPath: path,
	}, nil
}

// newPublicClient builds a client for the endpoints that take no credential
// (register, login, refresh).
func newPublicClient() (*apiClient, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return &apiClient{
		baseURL: resolveBaseURL(cfg),
		http:    &http.Client{Timeout: requestTimeout},
	}, nil
}

// do performs one request, transparently refreshing an expired access token once.
func (c *apiClient) do(method, path string, query url.Values, body any) ([]byte, error) {
	raw, err := c.doOnce(method, path, query, body)
	if err == nil {
		return raw, nil
	}

	failure, ok := err.(*apiError)
	if !ok || failure.Status != http.StatusUnauthorized || c.bearer == "" {
		return nil, err
	}
	if c.cfg == nil || strings.TrimSpace(c.cfg.RefreshToken) == "" {
		return nil, err
	}
	if refreshErr := c.refreshSession(); refreshErr != nil {
		// Report the original 401: the refresh failing means the session is gone.
		return nil, fmt.Errorf("%w (session refresh also failed: %v)", err, refreshErr)
	}
	return c.doOnce(method, path, query, body)
}

// refreshSession exchanges the stored refresh token for a new pair and persists
// it, so short-lived access tokens do not force a re-login between commands.
func (c *apiClient) refreshSession() error {
	payload := map[string]string{"refresh_token": c.cfg.RefreshToken}
	fresh := &apiClient{baseURL: c.baseURL, http: c.http}

	raw, err := fresh.doOnce("POST", "/api/auth/refresh", nil, payload)
	if err != nil {
		return err
	}

	var session authResponse
	if err := json.Unmarshal(raw, &session); err != nil {
		return err
	}
	if strings.TrimSpace(session.AccessToken) == "" {
		return fmt.Errorf("refresh returned no access token")
	}

	c.bearer = session.AccessToken
	c.cfg.AccessToken = session.AccessToken
	if strings.TrimSpace(session.RefreshToken) != "" {
		c.cfg.RefreshToken = session.RefreshToken
	}
	if c.cfgPath != "" {
		return saveConfig(c.cfg, c.cfgPath)
	}
	return nil
}

func (c *apiClient) doOnce(method, path string, query url.Values, body any) ([]byte, error) {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, endpoint, payload)
	if err != nil {
		return nil, err
	}
	switch {
	case c.bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	case c.apiKey != "":
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach sumi api at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &apiError{Status: resp.StatusCode, Message: extractAPIMessage(raw, resp.Status)}
	}
	return raw, nil
}

func extractAPIMessage(raw []byte, fallback string) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Message != "" {
		return envelope.Message
	}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
		return trimmed
	}
	return fallback
}

// emitJSON writes a machine-readable result to stdout. Every successful command
// prints exactly one JSON document so callers can pipe straight into a parser.
func emitJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if _, err := os.Stdout.Write(trimmed); err != nil {
		return err
	}
	_, err := os.Stdout.Write([]byte("\n"))
	return err
}

func emitValue(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return emitJSON(encoded)
}

// parseBillType maps the human-facing type names onto the API's 1/2 encoding.
func parseBillType(raw string) (int16, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "expense", "1", "支出":
		return 1, nil
	case "income", "2", "收入":
		return 2, nil
	default:
		return 0, fmt.Errorf("invalid --type %q: use expense or income", raw)
	}
}
