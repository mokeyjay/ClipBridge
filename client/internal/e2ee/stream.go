package e2ee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// gcmTagLen is the AES-GCM authentication tag length appended to each chunk.
const gcmTagLen = 16

// EncryptResult describes the produced ciphertext stream, mirroring the upload
// manifest fields the server verifies and the receiver needs.
type EncryptResult struct {
	ChunkSizeBytes      int
	TotalChunks         int
	CiphertextSizeBytes int64
	CiphertextSHA256    string // lowercase hex over the whole stream incl. tags
}

// newGCM builds an AES-256-GCM AEAD from a DEK.
func newGCM(dek []byte) (cipher.AEAD, error) {
	if len(dek) != DEKLen {
		return nil, fmt.Errorf("e2ee: 内容密钥长度 %d，应为 %d", len(dek), DEKLen)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("e2ee: 初始化分组密码: %w", err)
	}
	return cipher.NewGCM(block)
}

// EncryptStream encrypts plaintext from src into the chunked AEAD format on dst,
// using chunkSize plaintext blocks. It returns the manifest metadata. Empty input
// produces a single empty final chunk so total_chunks is always >= 1.
func EncryptStream(dst io.Writer, src io.Reader, dek []byte, hdr Header, chunkSize int) (EncryptResult, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return EncryptResult{}, err
	}

	hash := sha256.New()
	out := io.MultiWriter(dst, hash)

	cur := make([]byte, chunkSize)
	nxt := make([]byte, chunkSize)
	curN, err := readChunk(src, cur)
	if err != nil {
		return EncryptResult{}, err
	}

	var (
		index       uint64
		totalChunks int
		totalCipher int64
	)
	for {
		nxtN, err := readChunk(src, nxt)
		if err != nil {
			return EncryptResult{}, err
		}
		isFinal := nxtN == 0
		sealed := gcm.Seal(nil, chunkNonce(index), cur[:curN], chunkAAD(hdr, index, isFinal))
		if _, err := out.Write(sealed); err != nil {
			return EncryptResult{}, fmt.Errorf("e2ee: 写入密文: %w", err)
		}
		totalCipher += int64(len(sealed))
		totalChunks++
		index++
		if isFinal {
			break
		}
		cur, nxt = nxt, cur
		curN = nxtN
	}

	return EncryptResult{
		ChunkSizeBytes:      chunkSize,
		TotalChunks:         totalChunks,
		CiphertextSizeBytes: totalCipher,
		CiphertextSHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// DecryptStream decrypts a chunked AEAD stream from src into dst given the DEK and
// the manifest's chunkSize/totalChunks. Any authentication failure (wrong key,
// reorder, drop, truncation, AAD mismatch) aborts with an error and the partial
// output must be discarded by the caller.
func DecryptStream(dst io.Writer, src io.Reader, dek []byte, hdr Header, chunkSize, totalChunks int) error {
	if chunkSize <= 0 || totalChunks <= 0 {
		return fmt.Errorf("e2ee: 非法的分块参数 chunk=%d total=%d", chunkSize, totalChunks)
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return err
	}

	full := make([]byte, chunkSize+gcmTagLen)
	for index := 0; index < totalChunks; index++ {
		isFinal := index == totalChunks-1
		var ct []byte
		if isFinal {
			// The final chunk is whatever remains; it must be at least a tag.
			rest, err := io.ReadAll(src)
			if err != nil {
				return fmt.Errorf("e2ee: 读取末块密文: %w", err)
			}
			ct = rest
		} else {
			if _, err := io.ReadFull(src, full); err != nil {
				return fmt.Errorf("e2ee: 读取分块密文: %w", err)
			}
			ct = full
		}
		if len(ct) < gcmTagLen {
			return fmt.Errorf("e2ee: 第 %d 块密文过短", index)
		}
		plain, err := gcm.Open(nil, chunkNonce(uint64(index)), ct, chunkAAD(hdr, uint64(index), isFinal))
		if err != nil {
			return fmt.Errorf("e2ee: 第 %d 块认证失败: %w", index, err)
		}
		if _, err := dst.Write(plain); err != nil {
			return fmt.Errorf("e2ee: 写入明文: %w", err)
		}
	}
	// Ensure nothing trailing remains after the declared final chunk.
	if n, _ := io.Copy(io.Discard, src); n > 0 {
		return fmt.Errorf("e2ee: 末块后存在多余数据 %d 字节", n)
	}
	return nil
}

// readChunk fills buf as much as possible, treating a short read or EOF as the
// natural end of input (returning the bytes read and no error).
func readChunk(r io.Reader, buf []byte) (int, error) {
	n, err := io.ReadFull(r, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return n, nil
	}
	if err != nil {
		return n, fmt.Errorf("e2ee: 读取明文: %w", err)
	}
	return n, nil
}
