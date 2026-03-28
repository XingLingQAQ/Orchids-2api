package warp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"

	"orchids-api/internal/config"
	"orchids-api/internal/prompt"
	"orchids-api/internal/upstream"
	"orchids-api/internal/util"
)

type DiagnosticStep struct {
	Name        string            `json:"name"`
	Target      string            `json:"target,omitempty"`
	Proxy       string            `json:"proxy,omitempty"`
	OK          bool              `json:"ok"`
	StatusCode  int               `json:"status_code,omitempty"`
	DurationMS  int64             `json:"duration_ms"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyPreview string            `json:"body_preview,omitempty"`
	Error       string            `json:"error,omitempty"`
}

type DiagnosticResult struct {
	RefreshTokenSuffix string           `json:"refresh_token_suffix"`
	ProxyURL           string           `json:"proxy_url,omitempty"`
	DeviceID           string           `json:"device_id"`
	RequestID          string           `json:"request_id"`
	Model              string           `json:"model"`
	Prompt             string           `json:"prompt"`
	Steps              []DiagnosticStep `json:"steps"`
}

type DiagnosticOptions struct {
	RefreshToken string
	ProxyURL     string
	Model        string
	Prompt       string
	DeviceID     string
	RequestID    string
}

func RunDiagnostic(ctx context.Context, opts DiagnosticOptions) (*DiagnosticResult, error) {
	refreshToken := normalizeRefreshToken(opts.RefreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "warp-basic"
	}
	promptText := strings.TrimSpace(opts.Prompt)
	if promptText == "" {
		promptText = "hello from warpdiag"
	}

	cfg := &config.Config{
		DebugEnabled:   false,
		ProxyURL:       strings.TrimSpace(opts.ProxyURL),
		RequestTimeout: 120,
	}
	config.ApplyDefaults(cfg)
	if strings.TrimSpace(opts.ProxyURL) != "" {
		cfg.ProxyURL = strings.TrimSpace(opts.ProxyURL)
	}

	sess := getSession(0, refreshToken, opts.DeviceID, opts.RequestID)
	httpClient := newHTTPClient(0, cfg)
	authClient := newHTTPClient(0, cfg)
	httpClient.Jar = sess.jar
	authClient.Jar = sess.jar

	result := &DiagnosticResult{
		RefreshTokenSuffix: maskTokenSuffix(refreshToken),
		ProxyURL:           strings.TrimSpace(opts.ProxyURL),
		DeviceID:           sess.deviceID,
		RequestID:          sess.requestID,
		Model:              model,
		Prompt:             promptText,
	}

	refreshStep, jwt, err := runRefreshStep(ctx, sess, authClient, cfg)
	result.Steps = append(result.Steps, refreshStep)
	if err != nil {
		return result, nil
	}

	loginStep, err := runLoginStep(ctx, sess, httpClient, jwt, cfg)
	result.Steps = append(result.Steps, loginStep)
	if err != nil {
		return result, nil
	}

	aiStep := runAIStep(ctx, httpClient, jwt, model, promptText, cfg)
	result.Steps = append(result.Steps, aiStep)
	return result, nil
}

func runRefreshStep(ctx context.Context, sess *session, client *http.Client, cfg *config.Config) (DiagnosticStep, string, error) {
	step := DiagnosticStep{Name: "refresh", Target: warpFirebaseURL}
	step.Proxy = resolveDiagnosticProxy(cfg, warpFirebaseURL)
	start := time.Now()

	refreshToken := normalizeRefreshToken(sess.refreshToken)
	form := strings.NewReader("grant_type=refresh_token&refresh_token=" + url.QueryEscape(refreshToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, warpFirebaseURL, form)
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start), "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start), "", err
	}
	defer resp.Body.Close()

	body, readErr := readLimitedBody(resp, 1<<20)
	step.StatusCode = resp.StatusCode
	step.Headers = summarizeWarpResponseHeaders(resp.Header)
	step.BodyPreview = summarizeWarpErrorBody(string(body))
	if readErr != nil {
		step.Error = readErr.Error()
		return finishDiagnosticStep(step, start), "", readErr
	}
	if resp.StatusCode != http.StatusOK {
		step.Error = (&HTTPStatusError{Operation: "refresh token", StatusCode: resp.StatusCode}).Error()
		return finishDiagnosticStep(step, start), "", &HTTPStatusError{Operation: "refresh token", StatusCode: resp.StatusCode}
	}

	var parsed refreshResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start), "", err
	}
	jwt := strings.TrimSpace(parsed.IDToken)
	if jwt == "" {
		jwt = strings.TrimSpace(parsed.IDTokenAlt)
	}
	if jwt == "" {
		jwt = strings.TrimSpace(parsed.AccessToken)
	}
	if jwt == "" {
		step.Error = "warp refresh response missing id_token"
		return finishDiagnosticStep(step, start), "", fmt.Errorf("warp refresh response missing id_token")
	}

	sess.mu.Lock()
	sess.jwt = jwt
	sess.loggedIn = false
	sess.lastLogin = time.Time{}
	sess.mu.Unlock()

	step.OK = true
	return finishDiagnosticStep(step, start), jwt, nil
}

func runLoginStep(ctx context.Context, sess *session, client *http.Client, jwt string, cfg *config.Config) (DiagnosticStep, error) {
	step := DiagnosticStep{Name: "login", Target: warpLegacyLoginURL}
	step.Proxy = resolveDiagnosticProxy(cfg, warpLegacyLoginURL)
	start := time.Now()

	sess.mu.Lock()
	if strings.TrimSpace(sess.experimentID) == "" {
		sess.experimentID = newSessionUUID()
	}
	if strings.TrimSpace(sess.experimentBuck) == "" {
		sess.experimentBuck = newExperimentBucket()
	}
	experimentID := sess.experimentID
	experimentBuck := sess.experimentBuck
	sess.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, warpLegacyLoginURL, nil)
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start), err
	}
	req.Header.Set("X-Warp-Client-ID", clientID)
	req.Header.Set("X-Warp-Client-Version", clientVersion)
	req.Header.Set("X-Warp-OS-Category", clientOSCategory)
	req.Header.Set("X-Warp-OS-Name", clientOSName)
	req.Header.Set("X-Warp-OS-Version", clientOSVersion)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-Warp-Experiment-Id", experimentID)
	req.Header.Set("X-Warp-Experiment-Bucket", experimentBuck)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Content-Length", "0")

	resp, err := client.Do(req)
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start), err
	}
	defer resp.Body.Close()

	body, readErr := readLimitedBody(resp, 1<<20)
	step.StatusCode = resp.StatusCode
	step.Headers = summarizeWarpResponseHeaders(resp.Header)
	step.BodyPreview = summarizeWarpErrorBody(string(body))
	if readErr != nil {
		step.Error = readErr.Error()
		return finishDiagnosticStep(step, start), readErr
	}
	if resp.StatusCode != http.StatusNoContent {
		step.Error = (&HTTPStatusError{Operation: "login", StatusCode: resp.StatusCode}).Error()
		return finishDiagnosticStep(step, start), &HTTPStatusError{Operation: "login", StatusCode: resp.StatusCode}
	}

	sess.mu.Lock()
	sess.loggedIn = true
	sess.lastLogin = time.Now()
	sess.mu.Unlock()
	step.OK = true
	return finishDiagnosticStep(step, start), nil
}

func runAIStep(ctx context.Context, client *http.Client, jwt, model, promptText string, cfg *config.Config) DiagnosticStep {
	step := DiagnosticStep{Name: "ai", Target: warpLegacyAIURL}
	step.Proxy = resolveDiagnosticProxy(cfg, warpLegacyAIURL)
	start := time.Now()

	payloadPrompt, payload, err := buildRequestBytes(upstream.UpstreamRequest{
		Prompt:   promptText,
		Model:    model,
		Messages: []prompt.Message{{Role: "user", Content: prompt.MessageContent{Text: promptText}}},
	})
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start)
	}
	_ = payloadPrompt

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, warpLegacyAIURL, bytes.NewReader(payload))
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-Warp-Client-ID", clientID)
	req.Header.Set("X-Warp-Client-Version", clientVersion)
	req.Header.Set("X-Warp-OS-Category", clientOSCategory)
	req.Header.Set("X-Warp-OS-Name", clientOSName)
	req.Header.Set("X-Warp-OS-Version", clientOSVersion)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(payload)))
	req.Header.Set("User-Agent", "")

	resp, err := client.Do(req)
	if err != nil {
		step.Error = err.Error()
		return finishDiagnosticStep(step, start)
	}
	defer resp.Body.Close()

	body, readErr := readLimitedBody(resp, 4096)
	step.StatusCode = resp.StatusCode
	step.Headers = summarizeWarpResponseHeaders(resp.Header)
	step.BodyPreview = summarizeWarpErrorBody(string(body))
	if readErr != nil {
		step.Error = readErr.Error()
		return finishDiagnosticStep(step, start)
	}
	if resp.StatusCode == http.StatusOK {
		step.OK = true
		if strings.TrimSpace(step.BodyPreview) == "" {
			step.BodyPreview = "[stream accepted]"
		}
		return finishDiagnosticStep(step, start)
	}
	step.Error = (&HTTPStatusError{Operation: "stream request", StatusCode: resp.StatusCode}).Error()
	return finishDiagnosticStep(step, start)
}

func finishDiagnosticStep(step DiagnosticStep, start time.Time) DiagnosticStep {
	step.DurationMS = time.Since(start).Milliseconds()
	return step
}

func resolveDiagnosticProxy(cfg *config.Config, target string) string {
	if cfg == nil {
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	proxyFunc := util.ProxyFuncFromConfig(cfg)
	if proxyFunc == nil {
		return ""
	}
	u, err := proxyFunc(req)
	if err != nil || u == nil {
		return ""
	}
	return maskProxyURL(u)
}

func maskTokenSuffix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 12 {
		return token
	}
	return "..." + token[len(token)-12:]
}
