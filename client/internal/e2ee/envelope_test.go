package e2ee

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// testHeader returns a representative envelope header.
func testHeader() Header {
	return Header{
		ProtocolVersion: protocol.ProtocolVersion,
		ItemID:          "11111111-1111-1111-1111-111111111111",
		SourceDeviceID:  "22222222-2222-2222-2222-222222222222",
		ContentType:     protocol.ContentText,
	}
}

// encrypt is a test helper returning the ciphertext bytes and manifest metadata.
func encrypt(t *testing.T, plaintext []byte, dek []byte, hdr Header, chunkSize int) ([]byte, EncryptResult) {
	t.Helper()
	var buf bytes.Buffer
	res, err := EncryptStream(&buf, bytes.NewReader(plaintext), dek, hdr, chunkSize)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if int64(buf.Len()) != res.CiphertextSizeBytes {
		t.Fatalf("ciphertext size mismatch: buf=%d res=%d", buf.Len(), res.CiphertextSizeBytes)
	}
	return buf.Bytes(), res
}

// TestRoundTripSingleAndMultiChunk covers both the text (single-chunk) special
// case and multi-chunk bodies.
func TestRoundTripSingleAndMultiChunk(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()

	cases := map[string][]byte{
		"empty":       {},
		"short text":  []byte("hello clipbridge"),
		"exact chunk": bytes.Repeat([]byte("A"), 64),
		"multi chunk": bytes.Repeat([]byte("Z"), 64*3+7),
	}
	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			ct, res := encrypt(t, plaintext, dek, hdr, 64)
			var out bytes.Buffer
			if err := DecryptStream(&out, bytes.NewReader(ct), dek, hdr, res.ChunkSizeBytes, res.TotalChunks); err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if !bytes.Equal(out.Bytes(), plaintext) {
				t.Errorf("round-trip mismatch: got %q want %q", out.Bytes(), plaintext)
			}
		})
	}
}

// TestWrongDEKFails ensures a different content key cannot decrypt.
func TestWrongDEKFails(t *testing.T) {
	dek, _ := NewDEK()
	other, _ := NewDEK()
	hdr := testHeader()
	ct, res := encrypt(t, []byte("secret"), dek, hdr, 64)

	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(ct), other, hdr, res.ChunkSizeBytes, res.TotalChunks); err == nil {
		t.Error("decryption with wrong DEK unexpectedly succeeded")
	}
}

// TestAADMismatchFails ensures header tampering (e.g. forged source device) is
// detected by AAD binding.
func TestAADMismatchFails(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()
	ct, res := encrypt(t, []byte("secret"), dek, hdr, 64)

	tampered := hdr
	tampered.SourceDeviceID = "33333333-3333-3333-3333-333333333333"
	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(ct), dek, tampered, res.ChunkSizeBytes, res.TotalChunks); err == nil {
		t.Error("decryption with mismatched AAD header unexpectedly succeeded")
	}
}

// TestChunkReorderFails ensures swapping two chunks is rejected (the per-chunk
// index is bound in the AAD).
func TestChunkReorderFails(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()
	chunk := 64
	plaintext := bytes.Repeat([]byte("A"), chunk*3) // 3 full chunks + empty final? -> exact multiple
	ct, res := encrypt(t, plaintext, dek, hdr, chunk)
	if res.TotalChunks < 3 {
		t.Fatalf("expected >=3 chunks, got %d", res.TotalChunks)
	}

	// Swap the first two ciphertext chunks (each chunk+tag bytes).
	cs := chunk + gcmTagLen
	reordered := append([]byte(nil), ct...)
	copy(reordered[0:cs], ct[cs:2*cs])
	copy(reordered[cs:2*cs], ct[0:cs])

	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(reordered), dek, hdr, res.ChunkSizeBytes, res.TotalChunks); err == nil {
		t.Error("decryption of reordered chunks unexpectedly succeeded")
	}
}

// TestChunkDropFails ensures dropping a middle chunk is rejected (declared
// totalChunks no longer matches, and indices/final flag won't line up).
func TestChunkDropFails(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()
	chunk := 64
	ct, res := encrypt(t, bytes.Repeat([]byte("B"), chunk*3+5), dek, hdr, chunk)

	cs := chunk + gcmTagLen
	// Remove the first chunk's bytes but keep the declared totalChunks.
	dropped := append([]byte(nil), ct[cs:]...)
	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(dropped), dek, hdr, res.ChunkSizeBytes, res.TotalChunks); err == nil {
		t.Error("decryption with a dropped chunk unexpectedly succeeded")
	}
}

// TestTruncationFails ensures dropping the final chunk (and adjusting the count)
// is rejected because the last surviving chunk was not sealed as final.
func TestTruncationFails(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()
	chunk := 64
	ct, res := encrypt(t, bytes.Repeat([]byte("C"), chunk*3), dek, hdr, chunk)
	if res.TotalChunks < 2 {
		t.Fatalf("need >=2 chunks")
	}

	cs := chunk + gcmTagLen
	// Keep only the first totalChunks-1 chunks and claim that many chunks. The
	// new "last" chunk was sealed with is_final=false, so the final-flag AAD on
	// decryption won't match.
	truncated := ct[:cs*(res.TotalChunks-1)]
	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(truncated), dek, hdr, res.ChunkSizeBytes, res.TotalChunks-1); err == nil {
		t.Error("decryption of truncated stream unexpectedly succeeded")
	}
}

// TestWrapUnwrapFanOut covers DEK wrapping to two devices and unwrapping with the
// correct key, plus rejection with the wrong device's key/ID.
func TestWrapUnwrapFanOut(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()

	privA, pubA, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("gen A: %v", err)
	}
	privB, pubB, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("gen B: %v", err)
	}
	const devA, devB = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	wrappedA, err := WrapDEK(pubA, dek, hdr, devA)
	if err != nil {
		t.Fatalf("wrap A: %v", err)
	}
	wrappedB, err := WrapDEK(pubB, dek, hdr, devB)
	if err != nil {
		t.Fatalf("wrap B: %v", err)
	}

	// Correct unwrap recovers the same DEK.
	gotA, err := UnwrapDEK(privA, wrappedA, hdr, devA)
	if err != nil {
		t.Fatalf("unwrap A: %v", err)
	}
	if !bytes.Equal(gotA, dek) {
		t.Error("unwrapped DEK A mismatch")
	}
	gotB, err := UnwrapDEK(privB, wrappedB, hdr, devB)
	if err != nil || !bytes.Equal(gotB, dek) {
		t.Errorf("unwrap B mismatch: %v", err)
	}

	// B's key cannot open A's wrap.
	if _, err := UnwrapDEK(privB, wrappedA, hdr, devA); err == nil {
		t.Error("device B unwrapped device A's DEK")
	}
	// Right key but wrong bound device id fails (info mismatch).
	if _, err := UnwrapDEK(privA, wrappedA, hdr, devB); err == nil {
		t.Error("unwrap with mismatched device id succeeded")
	}
}

// TestKeyFingerprintStable checks the fingerprint is deterministic, shaped, and
// matches the shared protocol helper the server uses.
func TestKeyFingerprintStable(t *testing.T) {
	_, pub, _ := GenerateDeviceKey()
	fp1 := Fingerprint(pub)
	fp2 := protocol.KeyFingerprint(PublicKeyBase64(pub))
	if fp1 != fp2 {
		t.Errorf("client/server fingerprint disagree: %q vs %q", fp1, fp2)
	}
	if strings.Count(fp1, ":") != protocol.KeyFingerprintBytes-1 {
		t.Errorf("unexpected fingerprint shape: %q", fp1)
	}
}

// TestLargeBodyRoundTrip exercises a multi-megabyte streaming round-trip with the
// default chunk size to mimic file content.
func TestLargeBodyRoundTrip(t *testing.T) {
	dek, _ := NewDEK()
	hdr := testHeader()
	hdr.ContentType = protocol.ContentFile

	plaintext := make([]byte, 5*DefaultChunkSize+123)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatalf("rand: %v", err)
	}
	var ctBuf bytes.Buffer
	res, err := EncryptStream(&ctBuf, bytes.NewReader(plaintext), dek, hdr, DefaultChunkSize)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if res.TotalChunks != 6 {
		t.Errorf("total chunks = %d, want 6", res.TotalChunks)
	}
	var out bytes.Buffer
	if err := DecryptStream(&out, bytes.NewReader(ctBuf.Bytes()), dek, hdr, res.ChunkSizeBytes, res.TotalChunks); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Error("large body round-trip mismatch")
	}
}
