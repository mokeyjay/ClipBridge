// Package apiclient is the device-side HTTP/WSS client for the ClipBridge device
// port. It pins the server's self-signed certificate by SHA-256 fingerprint
// (TOFU established during pairing) and refuses to connect if the fingerprint
// changes, never silently trusting a new certificate. See prd/03-security-and-e2ee.md §3.
package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// ErrFingerprintMismatch is returned when the server's certificate fingerprint
// no longer matches the pinned value. The client must block and re-verify.
var ErrFingerprintMismatch = errors.New("apiclient: 服务器证书指纹与已固定值不一致")

// APIError carries a stable server error code so callers branch on Code, not text.
type APIError struct {
	Status int
	Code   protocol.ErrorCode
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("api %d %s: %s", e.Status, e.Code, e.Msg) }

// Client talks to one server's device port with a pinned certificate.
type Client struct {
	baseURL string
	token   string
	tlsConf *tls.Config
	hc      *http.Client
}

// New builds a client for baseURL (e.g. https://host:8443) pinned to the given
// device-port certificate SHA-256 fingerprint (colon-hex, case-insensitive).
func New(baseURL, pinnedFingerprint string) *Client {
	want := normalizeFingerprint(pinnedFingerprint)
	conf := &tls.Config{
		// We bypass the default chain/name validation (the cert is self-signed)
		// and instead pin the exact leaf certificate by fingerprint.
		InsecureSkipVerify: true, //nolint:gosec // pin check below is the real verification
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return ErrFingerprintMismatch
			}
			if normalizeFingerprint(protocol.CertFingerprint(cs.PeerCertificates[0].Raw)) != want {
				return ErrFingerprintMismatch
			}
			return nil
		},
		MinVersion: tls.VersionTLS12,
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		tlsConf: conf,
		hc: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{TLSClientConfig: conf},
		},
	}
}

// SetToken sets the device bearer token used for authenticated endpoints.
func (c *Client) SetToken(token string) { c.token = token }

// FetchServerFingerprint connects to a device port WITHOUT pinning and returns
// the SHA-256 fingerprint of the certificate it presents, formatted as uppercase
// colon-grouped hex. This is the first-contact TOFU step: the user compares this
// value against the Web pairing page before trusting it. It does not establish
// any trust by itself.
func FetchServerFingerprint(ctx context.Context, baseURL string) (string, error) {
	var fingerprint string
	conf := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // first-contact: we capture, the user verifies
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				fingerprint = protocol.CertFingerprint(cs.PeerCertificates[0].Raw)
			}
			return nil
		},
		MinVersion: tls.VersionTLS12,
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: conf}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	if fingerprint == "" {
		return "", errors.New("apiclient: 未取得服务器证书")
	}
	return fingerprint, nil
}

// normalizeFingerprint strips separators and uppercases so two fingerprints
// compare equal regardless of colon/space grouping or case.
func normalizeFingerprint(fp string) string {
	return strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(fp))
}

// url builds an absolute API URL from a path under /api/v1.
func (c *Client) url(path string) string { return c.baseURL + "/api/v1" + path }

// doJSON performs a JSON request/response with optional bearer/pairing auth.
func (c *Client) doJSON(ctx context.Context, method, path string, authz string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

// bearer returns the Authorization header value for the device token.
func (c *Client) bearer() string { return "Bearer " + c.token }

// decode reads a response, mapping non-2xx to APIError and 2xx JSON into out.
func decode(resp *http.Response, out any) error {
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env protocol.ErrorResponse
		_ = json.Unmarshal(data, &env)
		return &APIError{Status: resp.StatusCode, Code: env.Error.Code, Msg: env.Error.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// --- pairing (no device token yet) ---

// SubmitPairing submits a pairing request and returns the request id + poll token.
func (c *Client) SubmitPairing(ctx context.Context, req protocol.SubmitPairingRequest) (*protocol.SubmitPairingResponse, error) {
	var out protocol.SubmitPairingResponse
	if err := c.doJSON(ctx, http.MethodPost, "/pairing-requests", "", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollPairing polls a pairing request's status using its one-time poll token.
func (c *Client) PollPairing(ctx context.Context, requestID, pollToken string) (*protocol.PairingResultResponse, error) {
	var out protocol.PairingResultResponse
	if err := c.doJSON(ctx, http.MethodGet, "/pairing-requests/"+requestID, "Pairing "+pollToken, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- authenticated device endpoints ---

// GetTargets returns the user's currently online target devices and their keys.
func (c *Client) GetTargets(ctx context.Context) (*protocol.TargetsResponse, error) {
	var out protocol.TargetsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/device/targets", c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPeers 返回同用户的全部设备（含自身，Self 标记）与公钥指纹，供「关于」页
// 做跨设备人工互验展示。
func (c *Client) GetPeers(ctx context.Context) (*protocol.PeersResponse, error) {
	var out protocol.PeersResponse
	if err := c.doJSON(ctx, http.MethodGet, "/device/peers", c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IsFingerprintMismatch 判断错误是否为「服务器证书指纹与已固定值不一致」。
// TLS 握手错误会被 net/http、websocket 各层包裹，errors.Is 之外再兜底做一次
// 文本匹配，保证上层能稳定识别并进入引导式重置流程。
func IsFingerprintMismatch(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFingerprintMismatch) {
		return true
	}
	return strings.Contains(err.Error(), ErrFingerprintMismatch.Error())
}

// GetEffectiveConfig fetches the three-layer resolved policy this device enforces.
func (c *Client) GetEffectiveConfig(ctx context.Context) (*protocol.EffectiveConfig, error) {
	var out protocol.EffectiveConfig
	if err := c.doJSON(ctx, http.MethodGet, "/device/effective-config", c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDeviceSettings fetches the calling device's per-field inherit/override state.
func (c *Client) GetDeviceSettings(ctx context.Context) (*protocol.DeviceSettings, error) {
	var out protocol.DeviceSettings
	if err := c.doJSON(ctx, http.MethodGet, "/device/settings", c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDeviceSettings replaces the calling device's inherit/override state and
// returns the persisted result.
func (c *Client) UpdateDeviceSettings(ctx context.Context, ds protocol.DeviceSettings) (*protocol.DeviceSettings, error) {
	var out protocol.DeviceSettings
	if err := c.doJSON(ctx, http.MethodPatch, "/device/settings", c.bearer(), ds, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadItem streams a multipart upload (manifest + ciphertext) without buffering
// the whole ciphertext in memory.
func (c *Client) UploadItem(ctx context.Context, manifest protocol.UploadManifest, ciphertext io.Reader) (*protocol.UploadResponse, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		// Manifest part first, then the streamed ciphertext part.
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()
		mf, err := mw.CreateFormField("manifest")
		if err != nil {
			writeErr = err
			return
		}
		if writeErr = json.NewEncoder(mf).Encode(manifest); writeErr != nil {
			return
		}
		cf, err := mw.CreateFormField("ciphertext")
		if err != nil {
			writeErr = err
			return
		}
		if _, writeErr = io.Copy(cf, ciphertext); writeErr != nil {
			return
		}
		writeErr = mw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/clipboard/items"), pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", c.bearer())
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out protocol.UploadResponse
	if err := decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPending lists this device's pending, unexpired deliveries.
func (c *Client) GetPending(ctx context.Context) (*protocol.PendingDeliveriesResponse, error) {
	var out protocol.PendingDeliveriesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/clipboard/deliveries/pending", c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDelivery returns a single delivery manifest (with this device's wrapped DEK).
func (c *Client) GetDelivery(ctx context.Context, deliveryID string) (*protocol.DeliveryManifest, error) {
	var out protocol.DeliveryManifest
	if err := c.doJSON(ctx, http.MethodGet, "/clipboard/deliveries/"+deliveryID, c.bearer(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DownloadContent streams a delivery's ciphertext. The caller must Close the body.
func (c *Client) DownloadContent(ctx context.Context, deliveryID string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/clipboard/deliveries/"+deliveryID+"/content"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.bearer())
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, decode(resp, nil)
	}
	return resp.Body, nil
}

// Ack confirms a delivery was processed.
func (c *Client) Ack(ctx context.Context, deliveryID string) error {
	return c.doJSON(ctx, http.MethodPost, "/clipboard/deliveries/"+deliveryID+"/ack", c.bearer(), nil, nil)
}

// Reject rejects a delivery with an enumerated reason.
func (c *Client) Reject(ctx context.Context, deliveryID string, reason protocol.RejectReason) error {
	return c.doJSON(ctx, http.MethodPost, "/clipboard/deliveries/"+deliveryID+"/reject", c.bearer(),
		protocol.RejectRequest{Reason: reason}, nil)
}

// DialWS opens the device WSS connection (token auth + protocol negotiation),
// using the same pinned TLS configuration as the HTTP client.
func (c *Client) DialWS(ctx context.Context) (*websocket.Conn, error) {
	wsURL := "wss" + strings.TrimPrefix(c.baseURL, "https") +
		fmt.Sprintf("/api/v1/ws/device?protocol_version=%d", protocol.ProtocolVersion)
	dialer := &websocket.Dialer{TLSClientConfig: c.tlsConf, HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, wsURL, http.Header{"Authorization": {c.bearer()}})
	if err != nil {
		if resp != nil {
			return nil, &APIError{Status: resp.StatusCode, Code: protocol.ErrAuthRequired, Msg: "ws 握手失败"}
		}
		return nil, err
	}
	return conn, nil
}
