// Mobile push dispatch via Firebase Cloud Messaging HTTP v1 API.
//
// Service-account JSON is read from KUBECENTER_FCM_CREDENTIALS_PATH at
// startup; the dispatcher signs a short-lived OAuth bearer token using the
// service account's private key (golang-jwt/v5) and POSTs each notification
// to https://fcm.googleapis.com/v1/projects/<projectID>/messages:send.
//
// The OAuth token is cached for ~50 minutes; FCM tokens are valid an hour.
//
// When credentials are not configured, dispatch is a no-op that emits a
// structured warning — useful for non-mobile-enabled deployments and for
// PR-0 smoke tests where a real FCM project isn't available yet.

package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// FCMTokenURL is the OAuth2 token endpoint used to exchange a service-account
// JWT assertion for an access token.
const FCMTokenURL = "https://oauth2.googleapis.com/token"

// fcmScope is the OAuth scope required to send messages via FCM HTTP v1.
const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// FCMClient sends notifications via Firebase Cloud Messaging HTTP v1.
// A nil receiver value is safe: methods become no-ops with a warning, which
// matches "credentials not configured" deployments.
type FCMClient struct {
	projectID   string
	clientEmail string
	rawKey      []byte

	mu          sync.Mutex
	cachedToken string
	cachedUntil time.Time
}

// serviceAccount mirrors the well-known Google service-account JSON shape.
type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// LoadFCMClient reads the service-account JSON at credentialsPath and returns
// an FCMClient ready to dispatch. Returns (nil, nil) when credentialsPath is
// empty — the dispatcher treats that as "mobile push disabled" rather than
// an error so the backend boots cleanly without FCM.
func LoadFCMClient(credentialsPath string) (*FCMClient, error) {
	if credentialsPath == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read FCM credentials: %w", err)
	}

	var sa serviceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("parse FCM credentials: %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("FCM credentials missing project_id, client_email, or private_key")
	}

	return &FCMClient{
		projectID:   sa.ProjectID,
		clientEmail: sa.ClientEmail,
		rawKey:      []byte(sa.PrivateKey),
	}, nil
}

// accessToken returns a cached OAuth2 access token, refreshing it when
// within five minutes of expiry.
func (c *FCMClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Until(c.cachedUntil) > 5*time.Minute {
		return c.cachedToken, nil
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM(c.rawKey)
	if err != nil {
		return "", fmt.Errorf("parse FCM private key: %w", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   c.clientEmail,
		"scope": fcmScope,
		"aud":   FCMTokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := t.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign FCM JWT assertion: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", signed)

	req, err := http.NewRequestWithContext(ctx, "POST", FCMTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create FCM token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("FCM token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FCM token exchange status %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("decode FCM token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("FCM token response missing access_token")
	}

	c.cachedToken = tokenResp.AccessToken
	c.cachedUntil = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	return c.cachedToken, nil
}

// fcmMessage is the body shape for FCM HTTP v1 messages:send.
type fcmMessage struct {
	Message struct {
		Token        string            `json:"token"`
		Notification fcmNotification   `json:"notification"`
		Data         map[string]string `json:"data,omitempty"`
	} `json:"message"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// SendToToken delivers n to the given device token. The data payload is
// keyed for deep-link routing on the device:
//
//	cluster, kind, namespace, name (omitted when SuppressResourceFields)
//
// Errors from FCM (404 not_registered, 400 invalid_registration) bubble up
// so the caller can retire dead tokens. The mobile-push channel handler
// surfaces these errors via the existing channel last_error column.
func (c *FCMClient) SendToToken(ctx context.Context, deviceToken string, n Notification) error {
	if c == nil {
		return fmt.Errorf("FCM not configured")
	}

	tok, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	var msg fcmMessage
	msg.Message.Token = deviceToken
	msg.Message.Notification.Title = n.Title
	msg.Message.Notification.Body = n.Message
	msg.Message.Data = map[string]string{
		"source":    string(n.Source),
		"severity":  string(n.Severity),
		"clusterId": n.ClusterID,
	}
	if !n.SuppressResourceFields {
		msg.Message.Data["resourceKind"] = n.ResourceKind
		msg.Message.Data["resourceNamespace"] = n.ResourceNS
		msg.Message.Data["resourceName"] = n.ResourceName
	}

	body, _ := json.Marshal(msg)
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", c.projectID)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create FCM request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("FCM POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("FCM status %d: %s", resp.StatusCode, respBody)
}

// sendMobilePush dispatches n to every device registered for the channel's
// owner (the service account model: one mobile-push channel = one user's
// devices). Channel.Config["userId"] selects which user's devices to fan
// out to; absent userId falls back to the channel creator.
//
// Per-device send errors are aggregated and reported to the caller; FCM
// "not_registered" failures should be cleaned up by a periodic sweep
// (deferred to a follow-up PR — for PR-0 we just record the error).
func (s *NotificationService) sendMobilePush(ctx context.Context, ch Channel, n Notification) error {
	if s.fcm == nil {
		s.logger.Warn("mobile push channel dispatched but FCM not configured",
			"channel", ch.Name)
		return fmt.Errorf("FCM not configured")
	}

	userID, _ := ch.Config["userId"].(string)
	if userID == "" {
		userID = ch.CreatedBy
	}

	devices, err := s.store.ListDevicesForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	if len(devices) == 0 {
		return nil
	}

	var firstErr error
	for _, d := range devices {
		if err := s.fcm.SendToToken(ctx, d.DeviceToken, n); err != nil {
			s.logger.Warn("FCM send failed",
				"user", userID,
				"deviceId", d.ID,
				"platform", d.Platform,
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

