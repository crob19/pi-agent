package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	AuthEndpoint        = "https://auth.openai.com/oauth/authorize"
	TokenEndpoint       = "https://auth.openai.com/oauth/token"
	DeviceAuthEndpoint  = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	DeviceTokenEndpoint = "https://auth.openai.com/api/accounts/deviceauth/token"
	DeviceVerifyURL     = "https://auth.openai.com/codex/device"
	ClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	RedirectURI         = "http://localhost:1455/auth/callback"
	Scopes              = "openid profile email offline_access"
	CallbackPort        = 1455
)

// TokenResponse is the raw response from the OAuth token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// Credentials holds the resolved OAuth tokens and metadata.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id"`
}

// IsExpired returns true if the access token is expired or will expire within 5 minutes.
func (c *Credentials) IsExpired() bool {
	return time.Now().Unix() > c.ExpiresAt-300
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

func buildAuthorizationURL(codeChallenge, state string) string {
	params := url.Values{
		"client_id":                  {ClientID},
		"redirect_uri":               {RedirectURI},
		"scope":                      {Scopes},
		"code_challenge":             {codeChallenge},
		"code_challenge_method":      {"S256"},
		"response_type":              {"code"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"pi"},
	}
	return AuthEndpoint + "?" + params.Encode()
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}

func exchangeCodeForTokens(code, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"code_verifier": {codeVerifier},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.ErrorDescription != "" {
			return nil, fmt.Errorf("token exchange: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token exchange: %s", resp.Status)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tokenResp, nil
}

// RefreshToken exchanges a refresh token for a new access token.
func RefreshToken(refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		if errResp.Error == "invalid_grant" || strings.Contains(errResp.ErrorDescription, "revoked") {
			return nil, fmt.Errorf("refresh token expired or revoked: please re-authenticate")
		}
		if errResp.ErrorDescription != "" {
			return nil, fmt.Errorf("token refresh: %s - %s", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token refresh: %s", resp.Status)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tokenResp, nil
}

func extractAccountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}

	var claims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		Auth             *struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}

	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	if claims.ChatGPTAccountID != "" {
		return claims.ChatGPTAccountID
	}
	if claims.Auth != nil && claims.Auth.ChatGPTAccountID != "" {
		return claims.Auth.ChatGPTAccountID
	}
	if len(claims.Organizations) > 0 {
		return claims.Organizations[0].ID
	}
	return ""
}

// AuthenticateDevice runs the device code authorization flow, suitable for
// headless environments (e.g. a Raspberry Pi without a display). It prints
// a user code and URL, then polls until the user completes authentication
// on another device.
func AuthenticateDevice(ctx context.Context) (*Credentials, error) {
	// Step 1: Request a device/user code.
	body, err := json.Marshal(map[string]string{
		"client_id": ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling device auth request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", DeviceAuthEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("creating device auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device auth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Error != nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("device auth request failed: %s (%s)", resp.Status, errResp.Error.Message)
		}
		return nil, fmt.Errorf("device auth request failed: %s", resp.Status)
	}

	var deviceResp struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		IntervalRaw  json.RawMessage `json:"interval"`
		ExpiresInRaw json.RawMessage `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, fmt.Errorf("decoding device auth response: %w", err)
	}

	interval, err := parseJSONInt(deviceResp.IntervalRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid device auth interval: %w", err)
	}
	expiresIn, err := parseJSONInt(deviceResp.ExpiresInRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid device auth expires_in: %w", err)
	}

	// Step 2: Display instructions to the user.
	fmt.Println()
	fmt.Println("  To authenticate, visit:")
	fmt.Printf("    %s\n", DeviceVerifyURL)
	fmt.Println()
	fmt.Printf("  And enter code: %s\n", deviceResp.UserCode)
	fmt.Println()
	fmt.Println("  Waiting for authentication...")

	// Step 3: Poll for completion.
	pollInterval := time.Duration(interval+3) * time.Second // safety margin
	deadline := time.After(time.Duration(expiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("device authentication timed out")
		case <-time.After(pollInterval):
		}

		cred, done, err := pollDeviceToken(ctx, deviceResp.DeviceAuthID)
		if err != nil {
			return nil, err
		}
		if done {
			return cred, nil
		}
	}
}

func parseJSONInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing value")
	}

	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		v, convErr := strconv.Atoi(strings.TrimSpace(asString))
		if convErr != nil {
			return 0, convErr
		}
		return v, nil
	}

	return 0, fmt.Errorf("unsupported type: %s", string(raw))
}

func pollDeviceToken(ctx context.Context, deviceAuthID string) (*Credentials, bool, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":      ClientID,
		"device_auth_id": deviceAuthID,
	})
	if err != nil {
		return nil, false, fmt.Errorf("marshaling device token request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", DeviceTokenEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, false, fmt.Errorf("creating device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("device token request: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
		Error             string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, false, fmt.Errorf("decoding device token response: %w", err)
	}

	if tokenResp.Error == "authorization_pending" || tokenResp.AuthorizationCode == "" {
		return nil, false, nil // not ready yet
	}
	if tokenResp.Error != "" {
		return nil, false, fmt.Errorf("device auth error: %s", tokenResp.Error)
	}

	// Exchange the authorization code for tokens using the server-provided code verifier.
	oauthTokenResp, err := exchangeCodeForTokens(tokenResp.AuthorizationCode, tokenResp.CodeVerifier)
	if err != nil {
		return nil, false, err
	}

	accountID := ""
	if oauthTokenResp.IDToken != "" {
		accountID = extractAccountIDFromJWT(oauthTokenResp.IDToken)
	}
	if accountID == "" && oauthTokenResp.AccessToken != "" {
		accountID = extractAccountIDFromJWT(oauthTokenResp.AccessToken)
	}

	return &Credentials{
		AccessToken:  oauthTokenResp.AccessToken,
		RefreshToken: oauthTokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + int64(oauthTokenResp.ExpiresIn),
		AccountID:    accountID,
	}, true, nil
}

// Authenticate runs the full OAuth PKCE flow: opens the browser, waits
// for the callback, and returns credentials. The provided context controls
// the overall timeout.
func Authenticate(ctx context.Context) (*Credentials, error) {
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return nil, err
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	state, err := generateState()
	if err != nil {
		return nil, err
	}

	authURL := buildAuthorizationURL(codeChallenge, state)

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", CallbackPort))
	if err != nil {
		return nil, fmt.Errorf("starting callback server on port %d: %w", CallbackPort, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errChan <- fmt.Errorf("state mismatch: possible CSRF attack")
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errDesc := r.URL.Query().Get("error_description")
			errChan <- fmt.Errorf("oauth error: %s - %s", errMsg, errDesc)
			http.Error(w, errDesc, http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errChan <- fmt.Errorf("no authorization code received")
			http.Error(w, "No code received", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>`)
		codeChan <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("callback server: %w", err)
		}
	}()
	defer server.Shutdown(context.Background())

	fmt.Println("Opening browser for authentication...")
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser. Please visit this URL:\n%s\n", authURL)
	}

	select {
	case code := <-codeChan:
		tokenResp, err := exchangeCodeForTokens(code, codeVerifier)
		if err != nil {
			return nil, err
		}

		accountID := ""
		if tokenResp.IDToken != "" {
			accountID = extractAccountIDFromJWT(tokenResp.IDToken)
		}
		if accountID == "" && tokenResp.AccessToken != "" {
			accountID = extractAccountIDFromJWT(tokenResp.AccessToken)
		}

		return &Credentials{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
			AccountID:    accountID,
		}, nil

	case err := <-errChan:
		return nil, err

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	}
}
