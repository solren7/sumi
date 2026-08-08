package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// defaultKeyScopes is everything the sumi skill needs, so `auth key create --name x`
// alone yields a usable key. Narrow it with --scopes when the caller needs less.
var defaultKeyScopes = []string{
	"transactions:read",
	"transactions:write",
	"transactions:update",
	"transactions:delete",
	"categories:read",
	"categories:write",
	"stats:read",
}

type authResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID              string `json:"id"`
		Email           string `json:"email"`
		Username        string `json:"username"`
		DefaultCurrency string `json:"default_currency"`
		Timezone        string `json:"timezone"`
	} `json:"user"`
}

var authCmd = groupCommand("auth", "Log in and manage API keys, storing credentials locally")

// readPassword takes the password from stdin when --password-stdin is set, which
// keeps it out of the process list and shell history.
func readPassword(cmd *cobra.Command) (string, error) {
	fromStdin, _ := cmd.Flags().GetBool("password-stdin")
	inline, _ := cmd.Flags().GetString("password")

	if fromStdin {
		if strings.TrimSpace(inline) != "" {
			return "", fmt.Errorf("use either --password or --password-stdin, not both")
		}
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		password := strings.TrimRight(string(raw), "\r\n")
		if password == "" {
			return "", fmt.Errorf("no password on stdin")
		}
		return password, nil
	}

	if strings.TrimSpace(inline) == "" {
		return "", fmt.Errorf("password is required: pass --password-stdin (preferred) or --password")
	}
	return inline, nil
}

// persistSession stores the token pair and identity, replacing whatever session
// was there before.
func persistSession(session authResponse) (string, error) {
	cfg, path, err := loadConfig()
	if err != nil {
		return "", err
	}
	cfg.Email = session.User.Email
	cfg.AccessToken = session.AccessToken
	cfg.RefreshToken = session.RefreshToken
	if fromEnv := strings.TrimSpace(os.Getenv(envAPIURL)); fromEnv != "" {
		cfg.APIURL = strings.TrimRight(fromEnv, "/")
	}
	if err := saveConfig(cfg, path); err != nil {
		return "", err
	}
	return path, nil
}

func runSessionCommand(cmd *cobra.Command, path string, body map[string]string) error {
	password, err := readPassword(cmd)
	if err != nil {
		return err
	}
	email, _ := cmd.Flags().GetString("email")
	if strings.TrimSpace(email) == "" {
		return fmt.Errorf("--email is required")
	}

	client, err := newPublicClient()
	if err != nil {
		return err
	}

	body["email"] = strings.TrimSpace(email)
	body["password"] = password

	raw, err := client.do("POST", path, nil, body)
	if err != nil {
		return err
	}

	var session authResponse
	if err := unmarshalSession(raw, &session); err != nil {
		return err
	}

	configFile, err := persistSession(session)
	if err != nil {
		return err
	}

	// Deliberately does not echo the tokens; they are on disk now.
	return emitValue(map[string]any{
		"email":            session.User.Email,
		"username":         session.User.Username,
		"default_currency": session.User.DefaultCurrency,
		"timezone":         session.User.Timezone,
		"config":           configFile,
	})
}

var authRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Create an account and store its session locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]string{}
		if username, _ := cmd.Flags().GetString("username"); strings.TrimSpace(username) != "" {
			body["username"] = strings.TrimSpace(username)
		}
		return runSessionCommand(cmd, "/api/auth/register", body)
	},
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and store the session locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSessionCommand(cmd, "/api/auth/login", map[string]string{})
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke the stored session and clear local tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, path, err := loadConfig()
		if err != nil {
			return err
		}

		revoked := false
		if strings.TrimSpace(cfg.RefreshToken) != "" {
			client, err := newPublicClient()
			if err != nil {
				return err
			}
			// A failed revoke must not block clearing local state.
			if _, err := client.do("POST", "/api/auth/logout", nil, map[string]string{
				"refresh_token": cfg.RefreshToken,
			}); err == nil {
				revoked = true
			}
		}

		keepKey, _ := cmd.Flags().GetBool("keep-key")
		cfg.AccessToken = ""
		cfg.RefreshToken = ""
		if !keepKey {
			cfg.APIKey = ""
		}
		if err := saveConfig(cfg, path); err != nil {
			return err
		}

		return emitValue(map[string]any{
			"session_revoked": revoked,
			"api_key_cleared": !keepKey,
			"config":          path,
		})
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which credentials are in effect and where they came from",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, path, err := loadConfig()
		if err != nil {
			return err
		}

		_, statErr := os.Stat(path)
		status := map[string]any{
			"api_url":            resolveBaseURL(cfg),
			"config":             path,
			"config_exists":      statErr == nil,
			"email":              cfg.Email,
			"has_session":        strings.TrimSpace(cfg.AccessToken) != "",
			"api_key":            maskSecret(firstNonEmpty(os.Getenv(envAPIKey), cfg.APIKey)),
			"api_key_source":     credentialSource(envAPIKey, cfg.APIKey),
			"api_url_source":     credentialSource(envAPIURL, cfg.APIURL),
			"api_key_configured": strings.TrimSpace(firstNonEmpty(os.Getenv(envAPIKey), cfg.APIKey)) != "",
		}

		// Prove the credential actually works rather than only reporting its presence.
		if status["api_key_configured"].(bool) {
			client, clientErr := newAPIClient()
			if clientErr != nil {
				status["api_key_valid"] = false
			} else if _, callErr := client.do("GET", "/api/categories/", url.Values{"type": []string{"1"}}, nil); callErr != nil {
				status["api_key_valid"] = false
				status["api_key_error"] = callErr.Error()
			} else {
				status["api_key_valid"] = true
			}
		}

		return emitValue(status)
	},
}

var authKeyCmd = groupCommand("key", "Manage API keys (requires a stored session)")

var authKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an API key and store it locally for later commands",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("--name is required")
		}

		scopes, _ := cmd.Flags().GetStringSlice("scopes")
		if len(scopes) == 0 {
			scopes = defaultKeyScopes
		}

		client, err := newAuthClient()
		if err != nil {
			return err
		}

		body := map[string]any{"name": strings.TrimSpace(name), "scopes": scopes}
		if expires, _ := cmd.Flags().GetString("expires-at"); strings.TrimSpace(expires) != "" {
			body["expires_at"] = strings.TrimSpace(expires)
		}

		raw, err := client.do("POST", "/api/api-keys/", nil, body)
		if err != nil {
			return err
		}

		var created struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			Prefix string   `json:"prefix"`
			Scopes []string `json:"scopes"`
			Key    string   `json:"key"`
		}
		if err := unmarshalSession(raw, &created); err != nil {
			return err
		}

		configFile := ""
		if save, _ := cmd.Flags().GetBool("save"); save {
			cfg, path, err := loadConfig()
			if err != nil {
				return err
			}
			cfg.APIKey = created.Key
			if err := saveConfig(cfg, path); err != nil {
				return err
			}
			configFile = path
		}

		// The plaintext key is returned only once by the API, so it is printed here
		// even though it was also stored: the caller may need to inject it elsewhere.
		return emitValue(map[string]any{
			"id":     created.ID,
			"name":   created.Name,
			"prefix": created.Prefix,
			"scopes": created.Scopes,
			"key":    created.Key,
			"config": configFile,
		})
	},
}

var authKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAuthClient()
		if err != nil {
			return err
		}
		raw, err := client.do("GET", "/api/api-keys/", nil, nil)
		if err != nil {
			return err
		}
		return emitJSON(raw)
	},
}

// authKeyRevokeCmd is the only kill switch offered, because the API's DELETE
// endpoint is an alias of revoke: the row is kept with status "revoked" and still
// shows up in `key list`. Exposing a `del` command would imply a removal that
// does not happen.
var authKeyRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an API key (it stays listed with status revoked)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAuthClient()
		if err != nil {
			return err
		}
		if _, err := client.do("POST", "/api/api-keys/"+url.PathEscape(args[0])+"/revoke", nil, nil); err != nil {
			return err
		}
		return emitValue(map[string]any{"revoked": true, "id": args[0]})
	},
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func credentialSource(envName, stored string) string {
	if strings.TrimSpace(os.Getenv(envName)) != "" {
		return "env:" + envName
	}
	if strings.TrimSpace(stored) != "" {
		return "config"
	}
	return "unset"
}

func init() {
	for _, cmd := range []*cobra.Command{authRegisterCmd, authLoginCmd} {
		cmd.Flags().String("email", "", "Account email (required)")
		cmd.Flags().String("password", "", "Password (visible in the process list; prefer --password-stdin)")
		cmd.Flags().Bool("password-stdin", false, "Read the password from stdin")
	}
	authRegisterCmd.Flags().String("username", "", "Display name (default: the local part of the email)")

	authLogoutCmd.Flags().Bool("keep-key", false, "Keep the stored API key, only drop the session")

	authKeyCreateCmd.Flags().String("name", "", "Label for the key (required)")
	authKeyCreateCmd.Flags().StringSlice("scopes", nil, "Comma-separated scopes (default: every scope the sumi skill needs)")
	authKeyCreateCmd.Flags().String("expires-at", "", "Expiry as RFC3339 (default: never)")
	authKeyCreateCmd.Flags().Bool("save", true, "Store the key in the local config for later commands")

	authKeyCmd.AddCommand(authKeyCreateCmd, authKeyListCmd, authKeyRevokeCmd)
	authCmd.AddCommand(authRegisterCmd, authLoginCmd, authLogoutCmd, authStatusCmd, authKeyCmd)
	rootView.AddCommand(authCmd)
}

// unmarshalSession decodes a response body, turning a shape mismatch into a clear
// error instead of a zero-valued struct.
func unmarshalSession(raw []byte, target any) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unexpected response from the server: %w", err)
	}
	return nil
}
