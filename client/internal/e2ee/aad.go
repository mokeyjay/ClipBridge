package e2ee

import "encoding/binary"

// AAD domain-separation labels. The trailing version byte lets the format evolve
// without ambiguity. The chunk label binds the body; the wrap label binds each
// per-device DEK envelope.
var (
	chunkAADLabel = []byte("clipbridge/aad/chunk\x01")
	wrapInfoLabel = []byte("clipbridge/info/wrap\x01")
)

// appendField appends a 4-byte big-endian length prefix followed by f, making the
// concatenation of variable-length fields unambiguous.
func appendField(b, f []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(f)))
	b = append(b, n[:]...)
	return append(b, f...)
}

// appendHeader appends the common immutable context fields (prd §6.4): protocol
// version, content UUID, source device UUID and content type.
func appendHeader(b []byte, hdr Header) []byte {
	var pv [2]byte
	binary.BigEndian.PutUint16(pv[:], uint16(hdr.ProtocolVersion))
	b = appendField(b, pv[:])
	b = appendField(b, []byte(hdr.ItemID))
	b = appendField(b, []byte(hdr.SourceDeviceID))
	b = appendField(b, []byte(hdr.ContentType))
	return b
}

// chunkAAD builds the AAD for body chunk index, additionally binding the chunk
// index and is_final flag so chunks cannot be reordered, dropped or truncated.
func chunkAAD(hdr Header, index uint64, isFinal bool) []byte {
	b := append([]byte(nil), chunkAADLabel...)
	b = appendHeader(b, hdr)
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], index)
	b = appendField(b, idx[:])
	var fin byte
	if isFinal {
		fin = 1
	}
	return append(b, fin)
}

// wrapInfo builds the HPKE info for a per-device DEK wrap, binding the target
// device UUID in addition to the common header (prd §6.4).
func wrapInfo(hdr Header, targetDeviceID string) []byte {
	b := append([]byte(nil), wrapInfoLabel...)
	b = appendHeader(b, hdr)
	return appendField(b, []byte(targetDeviceID))
}

// chunkNonce derives the 12-byte AES-GCM nonce for a chunk index. Safe because
// the DEK is unique per content, so (key, nonce) never repeats.
func chunkNonce(index uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], index)
	return nonce
}
