package main

// Apple Push Notification service, token-based: the one way to reach a phone whose app is not running. The owner
// makes an APNs key in their Apple developer account and gives the add-on its id, their team id and the key; the
// add-on signs a short-lived token with it and posts each notification to Apple. Nothing else leaves the house:
// Apple sees the device token, the title and the body of the alert, as it must to deliver them.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type apnsClient struct {
	teamID, keyID string
	key           *ecdsa.PrivateKey
	bundle        string
	client        *http.Client
	// Where to post: Apple's servers by environment, or, in a development run (ATRIUM_APNS_DIR), a folder that
	// collects the payloads for `xcrun simctl push`.
	production, sandbox, dir string
	// With no key of its own: the publisher's relay, which holds the key and signs for every household
	// (POST <relay>/v1/push). Apple then sees the same as before; the relay sees the device token and the payload.
	relay string

	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// newAPNS reads the owner's key. nil when the options name none: alerts are then logged and not sent.
func newAPNS(o Options) (*apnsClient, error) {
	if o.APNSKey == "" && os.Getenv("ATRIUM_APNS_DIR") == "" {
		if o.PushRelay == "" {
			return nil, nil
		}
		u, err := url.Parse(o.PushRelay)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("push_relay must be an https address, like https://push.example.com")
		}
		return &apnsClient{relay: strings.TrimSuffix(o.PushRelay, "/"), client: &http.Client{Timeout: 20 * time.Second}}, nil
	}
	c := &apnsClient{teamID: o.APNSTeamID, keyID: o.APNSKeyID, bundle: firstNonEmpty(o.APNSBundle, "com.cmmichael.atrium"),
		client: &http.Client{Timeout: 20 * time.Second}, production: "https://api.push.apple.com", sandbox: "https://api.sandbox.push.apple.com", dir: os.Getenv("ATRIUM_APNS_DIR")}
	if c.dir != "" {
		os.MkdirAll(c.dir, 0o755)
		return c, nil
	}
	if c.teamID == "" || c.keyID == "" {
		return nil, fmt.Errorf("apns_team_id and apns_key_id go with apns_key")
	}
	block, _ := pem.Decode([]byte(strings.ReplaceAll(o.APNSKey, `\n`, "\n")))
	if block == nil {
		return nil, fmt.Errorf("apns_key is not a PEM key (the contents of the AuthKey_%s.p8 file)", c.keyID)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns_key: %v", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("apns_key is not an EC key")
	}
	c.key = key
	return c, nil
}

// jwt is the provider token Apple wants: ES256 over {iss: team, iat: now}, good for an hour; renewed after 50 min.
func (c *apnsClient) jwt() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Since(c.tokenAt) < 50*time.Minute {
		return c.token, nil
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	head := enc(map[string]string{"alg": "ES256", "kid": c.keyID})
	claims := enc(map[string]any{"iss": c.teamID, "iat": time.Now().Unix()})
	digest := sha256.Sum256([]byte(head + "." + claims))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	c.token = head + "." + claims + "." + base64.RawURLEncoding.EncodeToString(sig)
	c.tokenAt = time.Now()
	return c.token, nil
}

// push is one notification to one device. pushType is alert, background or liveactivity; collapse, when set, lets a
// later notification replace an earlier one with the same id.
type push struct {
	token, env, pushType, collapse string
	priority                       int
	payload                        any
}

type apnsError struct {
	status int
	reason string
}

func (e *apnsError) Error() string { return fmt.Sprintf("APNs answered %d %s", e.status, e.reason) }

func (c *apnsClient) send(p push) error {
	topic := c.bundle
	if p.pushType == "liveactivity" {
		topic += ".push-type.liveactivity"
	}
	body, _ := json.Marshal(p.payload)
	if c.dir != "" {
		// Development: the payload as a file, headers first, for `xcrun simctl push <device> <bundle> <file>`.
		name := fmt.Sprintf("%d-%s-%s.json", time.Now().UnixMilli(), p.pushType, shortToken(p.token))
		meta := map[string]any{"Simulator Target Bundle": c.bundle, "apns-topic": topic, "apns-push-type": p.pushType, "apns-collapse-id": p.collapse}
		var out map[string]any
		json.Unmarshal(body, &out)
		for k, v := range meta {
			if v != "" {
				out["_"+k] = v
			}
		}
		out["Simulator Target Bundle"] = c.bundle
		data, _ := json.MarshalIndent(out, "", "  ")
		return os.WriteFile(filepath.Join(c.dir, name), data, 0o644)
	}
	if c.relay != "" {
		return c.sendByRelay(p, body)
	}
	token, err := c.jwt()
	if err != nil {
		return err
	}
	base := c.production
	if p.env == "sandbox" {
		base = c.sandbox
	}
	req, err := http.NewRequest("POST", base+"/3/device/"+p.token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", topic)
	req.Header.Set("apns-push-type", p.pushType)
	req.Header.Set("apns-priority", fmt.Sprint(firstNonZero(p.priority, 10)))
	req.Header.Set("apns-expiration", "0")
	if p.collapse != "" {
		req.Header.Set("apns-collapse-id", p.collapse)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		return nil
	}
	var e struct {
		Reason string `json:"reason"`
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	json.Unmarshal(data, &e)
	return &apnsError{res.StatusCode, e.Reason}
}

// sendByRelay hands one push to the relay, which answers with Apple's own status and reason.
func (c *apnsClient) sendByRelay(p push, payload []byte) error {
	env := "production"
	if p.env == "sandbox" {
		env = "sandbox"
	}
	priority := p.priority
	if priority == 0 {
		priority = 10
	}
	body, _ := json.Marshal(map[string]any{"token": p.token, "env": env, "pushType": p.pushType, "collapse": p.collapse,
		"priority": priority, "payload": json.RawMessage(payload)})
	res, err := c.client.Post(c.relay+"/v1/push", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		return nil
	}
	var e struct {
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	json.Unmarshal(data, &e)
	return &apnsError{res.StatusCode, firstNonEmpty(e.Reason, e.Error, "from the relay")}
}

func shortToken(t string) string {
	if len(t) > 8 {
		return t[:8]
	}
	return t
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}
