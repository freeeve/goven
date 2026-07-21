package repo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// masterPasswordKey is the fixed passphrase Maven uses to encrypt the master
// password itself inside settings-security.xml.
const masterPasswordKey = "settings.security"

// xmlSettingsSecurity decodes settings-security.xml.
type xmlSettingsSecurity struct {
	Master     string `xml:"master"`
	Relocation string `xml:"relocation"`
}

// DefaultSecurityPath returns ~/.m2/settings-security.xml.
func DefaultSecurityPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".m2", "settings-security.xml")
}

// LoadMasterPassword reads settings-security.xml (following one level of
// <relocation>) and returns the decrypted master password, or "" when the
// file does not exist.
func LoadMasterPassword(path string) (string, error) {
	for range 2 {
		if path == "" {
			return "", nil
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var x xmlSettingsSecurity
		if err := xml.Unmarshal(data, &x); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if x.Relocation != "" && x.Master == "" {
			path = x.Relocation
			continue
		}
		if x.Master == "" {
			return "", nil
		}
		master, err := DecryptPassword(x.Master, masterPasswordKey)
		if err != nil {
			return "", fmt.Errorf("%s: decrypt master: %w", path, err)
		}
		return master, nil
	}
	return "", fmt.Errorf("settings-security relocation loop")
}

// extractEncrypted returns the payload between the outermost braces of a
// Maven-encrypted value ("{base64}") and whether the value is encrypted at
// all. A brace escaped as \{ marks a literal value.
func extractEncrypted(value string) (string, bool) {
	start := strings.Index(value, "{")
	end := strings.LastIndex(value, "}")
	if start < 0 || end <= start {
		return "", false
	}
	if start > 0 && value[start-1] == '\\' {
		return "", false
	}
	return value[start+1 : end], true
}

// DecryptPassword decrypts one {base64} payload (braces optional) with the
// given passphrase, implementing plexus-cipher's PBECipher: the base64 body
// is salt(8) | padLen(1) | ciphertext, AES-128-CBC, with key and IV taken
// from SHA-256(passphrase | salt).
func DecryptPassword(value, passphrase string) (string, error) {
	payload := value
	if inner, ok := extractEncrypted(value); ok {
		payload = inner
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", fmt.Errorf("decode encrypted value: %w", err)
	}
	if len(raw) < 10 {
		return "", fmt.Errorf("encrypted value too short (%d bytes)", len(raw))
	}
	salt := raw[:8]
	padLen := int(raw[8])
	if padLen < 0 || len(raw)-9-padLen <= 0 {
		return "", fmt.Errorf("encrypted value has invalid padding length %d", padLen)
	}
	ciphertext := raw[9 : len(raw)-padLen]
	if len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext length %d is not block-aligned", len(ciphertext))
	}

	keyAndIv := sha256.Sum256(append([]byte(passphrase), salt...))
	block, err := aes.NewCipher(keyAndIv[:16])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, keyAndIv[16:32]).CryptBlocks(plain, ciphertext)

	// Strip PKCS5 padding.
	n := len(plain)
	pkcs := int(plain[n-1])
	if pkcs < 1 || pkcs > aes.BlockSize || pkcs > n {
		return "", fmt.Errorf("wrong passphrase or corrupt value (bad PKCS5 padding)")
	}
	for _, b := range plain[n-pkcs:] {
		if int(b) != pkcs {
			return "", fmt.Errorf("wrong passphrase or corrupt value (bad PKCS5 padding)")
		}
	}
	return string(plain[:n-pkcs]), nil
}

// ResolvePassword resolves a server password: encrypted values are decrypted
// with the master password; plaintext values (including \{escaped) pass
// through.
func ResolvePassword(value, master string) (string, error) {
	if _, ok := extractEncrypted(value); !ok {
		return value, nil
	}
	if master == "" {
		return "", fmt.Errorf("password is encrypted but no settings-security.xml master password is available")
	}
	return DecryptPassword(value, master)
}
