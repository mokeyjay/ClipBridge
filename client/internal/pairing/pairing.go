// Package pairing orchestrates the device-side pairing flow: it generates the
// device HPKE key pair, submits a pairing request with a user-entered 6-digit
// code, polls until the user confirms in the Web console, then persists the
// device identity, token, private key and pinned server fingerprint. The pinned
// fingerprint must be confirmed by the user against the Web pairing page before
// calling Run (TOFU trust root). See prd/05-api-and-events.md §6.
package pairing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mokeyjay/clipbridge/client/internal/apiclient"
	"github.com/mokeyjay/clipbridge/client/internal/credstore"
	"github.com/mokeyjay/clipbridge/client/internal/e2ee"
	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// ErrRejected / ErrExpired report terminal pairing outcomes.
var (
	ErrRejected = errors.New("pairing: 配对请求被拒绝")
	ErrExpired  = errors.New("pairing: 配对请求已过期")
)

// Request carries the inputs the user supplies to pair a new device.
type Request struct {
	ServerURL         string // device-port base URL, e.g. https://host:8443
	ServerFingerprint string // user-confirmed device-port cert SHA-256
	Code              string // 6-digit pairing code from the Web console
	DeviceName        string
	Platform          protocol.Platform
	ClientVersion     string
}

// pollInterval / pollTimeout bound how long Run waits for Web confirmation.
const (
	pollInterval = 2 * time.Second
	pollTimeout  = 5 * time.Minute
)

// Run executes the full pairing flow and persists credentials on success. It
// generates a fresh key pair, saving the private key before submitting so a
// crash mid-flow never leaves a registered public key without its private half.
func Run(ctx context.Context, store *credstore.Store, req Request) (*credstore.Identity, error) {
	return run(ctx, store, req, pollInterval)
}

// run is Run with an injectable poll interval for tests.
func run(ctx context.Context, store *credstore.Store, req Request, interval time.Duration) (*credstore.Identity, error) {
	priv, pub, err := e2ee.GenerateDeviceKey()
	if err != nil {
		return nil, err
	}
	if err := store.SavePrivateKey(priv); err != nil {
		return nil, err
	}
	pubB64 := e2ee.PublicKeyBase64(pub)

	client := apiclient.New(req.ServerURL, req.ServerFingerprint)
	submitted, err := client.SubmitPairing(ctx, protocol.SubmitPairingRequest{
		Code: req.Code, DeviceName: req.DeviceName, Platform: string(req.Platform),
		ClientVersion: req.ClientVersion, HPKEPublicKey: pubB64,
	})
	if err != nil {
		return nil, fmt.Errorf("pairing: 提交配对请求: %w", err)
	}

	deadline := time.Now().Add(pollTimeout)
	for {
		result, err := client.PollPairing(ctx, submitted.RequestID, submitted.PollToken)
		if err != nil {
			return nil, fmt.Errorf("pairing: 轮询配对结果: %w", err)
		}
		switch result.Status {
		case protocol.PairingRequestConfirmed:
			if result.Device == nil || result.DeviceToken == "" {
				return nil, errors.New("pairing: 确认响应缺少设备或令牌")
			}
			return persist(store, req, pubB64, result)
		case protocol.PairingRequestRejected:
			return nil, ErrRejected
		case protocol.PairingRequestExpired:
			return nil, ErrExpired
		}
		if time.Now().After(deadline) {
			return nil, ErrExpired
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// persist saves identity, token and pinned fingerprint after confirmation.
func persist(store *credstore.Store, req Request, pubB64 string, result *protocol.PairingResultResponse) (*credstore.Identity, error) {
	id := &credstore.Identity{
		DeviceID:     result.Device.ID,
		UserID:       result.Device.UserID,
		ServerID:     result.Device.ServerID,
		ServerURL:    req.ServerURL,
		PublicKeyB64: pubB64,
		Version:      1,
	}
	if err := store.SaveIdentity(id); err != nil {
		return nil, err
	}
	if err := store.SaveToken(result.DeviceToken); err != nil {
		return nil, err
	}
	if err := store.SaveServerFingerprint(req.ServerFingerprint); err != nil {
		return nil, err
	}
	return id, nil
}
