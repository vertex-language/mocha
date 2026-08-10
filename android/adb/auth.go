package adb

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

func adbHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("adb: locate home directory: %w", err)
	}
	return filepath.Join(home, ".android"), nil
}

// loadOrCreateHostKey loads ~/.android/adbkey, generating and persisting a
// new 2048-bit RSA key on first use — the same convention the reference
// adb client follows.
func loadOrCreateHostKey() (*rsa.PrivateKey, error) {
	dir, err := adbHomeDir()
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "adbkey")

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("adb: %s is not a valid PEM key", keyPath)
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("adb: parse %s: %w", keyPath, err)
		}
		return key, nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("adb: generate host key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err == nil {
		block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
		_ = os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)
	}
	return key, nil
}

// signAuthToken signs the 20-byte AUTH(TOKEN) challenge with RSASSA-PKCS1-v1.5
// under SHA-1, as the adbd daemon expects.
func signAuthToken(key *rsa.PrivateKey, token []byte) ([]byte, error) {
	if len(token) != 20 {
		return nil, fmt.Errorf("%w: auth token is %d bytes, want 20", ErrProtocol, len(token))
	}
	return rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA1, token)
}

// marshalADBPublicKey encodes pub in adb's custom RSAPublicKey wire format
// (word-count, n0inv, modulus, R² mod N, exponent — all little-endian),
// base64-encoded with a trailing "user@host" comment, as sent in
// AUTH(RSAPUBLICKEY) and written to adbkey.pub.
func marshalADBPublicKey(priv *rsa.PrivateKey) ([]byte, error) {
	n := priv.PublicKey.N
	bitLen := n.BitLen()
	words := (bitLen + 31) / 32
	byteLen := words * 4

	modBytes := make([]byte, byteLen)
	nBytes := n.Bytes()
	copy(modBytes[byteLen-len(nBytes):], nBytes)
	reverseBytes(modBytes)

	word0 := binary.LittleEndian.Uint32(modBytes[0:4])
	mod32 := new(big.Int).Lsh(big.NewInt(1), 32)
	inv := new(big.Int).ModInverse(new(big.Int).SetUint64(uint64(word0)), mod32)
	if inv == nil {
		return nil, fmt.Errorf("adb: RSA modulus has no inverse mod 2^32")
	}
	n0inv := uint32(new(big.Int).Sub(mod32, inv).Uint64())

	r := new(big.Int).Lsh(big.NewInt(1), uint(byteLen*8))
	rr := new(big.Int).Mod(new(big.Int).Mul(r, r), n)
	rrBytes := make([]byte, byteLen)
	rrBig := rr.Bytes()
	copy(rrBytes[byteLen-len(rrBig):], rrBig)
	reverseBytes(rrBytes)

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(words))
	binary.Write(buf, binary.LittleEndian, n0inv)
	buf.Write(modBytes)
	buf.Write(rrBytes)
	binary.Write(buf, binary.LittleEndian, uint32(priv.PublicKey.E))

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return []byte(encoded + " mocha@adb\x00"), nil
}

func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}