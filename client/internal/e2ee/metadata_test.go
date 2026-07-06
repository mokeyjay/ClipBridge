package e2ee

import (
	"bytes"
	"testing"

	"github.com/mokeyjay/clipbridge/shared/protocol"
)

// TestMetadataRoundTrip checks sealed metadata decrypts with the right DEK+header.
func TestMetadataRoundTrip(t *testing.T) {
	dek, _ := NewDEK()
	hdr := Header{ProtocolVersion: protocol.ProtocolVersion, ItemID: "i1", SourceDeviceID: "s1", ContentType: protocol.ContentImage}
	meta := []byte(`{"filename":"photo.png","width":1920,"height":1080}`)

	sealed, err := SealMetadata(dek, hdr, meta)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("photo.png")) {
		t.Error("sealed metadata leaks plaintext filename")
	}
	got, err := OpenMetadata(dek, hdr, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, meta) {
		t.Errorf("round-trip mismatch: %q", got)
	}
}

// TestMetadataWrongKeyOrHeaderFails checks the DEK and header are both bound.
func TestMetadataWrongKeyOrHeaderFails(t *testing.T) {
	dek, _ := NewDEK()
	other, _ := NewDEK()
	hdr := Header{ProtocolVersion: 1, ItemID: "i1", SourceDeviceID: "s1", ContentType: protocol.ContentFile}
	sealed, _ := SealMetadata(dek, hdr, []byte("secret.txt"))

	if _, err := OpenMetadata(other, hdr, sealed); err == nil {
		t.Error("wrong DEK opened metadata")
	}
	tampered := hdr
	tampered.ItemID = "i2"
	if _, err := OpenMetadata(dek, tampered, sealed); err == nil {
		t.Error("mismatched header opened metadata")
	}
}

// TestMetadataNonceDistinctFromBody ensures the metadata nonce never equals a
// body-chunk nonce (guards against (key,nonce) reuse).
func TestMetadataNonceDistinctFromBody(t *testing.T) {
	mn := metaNonce()
	for i := uint64(0); i < 4; i++ {
		if bytes.Equal(mn, chunkNonce(i)) {
			t.Fatalf("metadata nonce collides with chunk %d nonce", i)
		}
	}
}
