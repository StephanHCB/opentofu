package aes256state

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/opentofu/opentofu/internal/states/statecrypto/cryptoconfig"
	"io"
	"log"
	"regexp"
)

const ClientSide_Aes256cfb_Sha256 = "client-side/AES256-CFB/SHA256"

func Metadata() cryptoconfig.MethodMetadata {
	return cryptoconfig.MethodMetadata{
		Name:        ClientSide_Aes256cfb_Sha256,
		Constructor: constructor,
	}
}

func constructor(configuration cryptoconfig.Config) (cryptoconfig.Method, error) {
	return &AES256CFBMethod{}, nil
}

type AES256CFBMethod struct {
}

func parseKey(hexKey string) ([]byte, error) {
	validator := regexp.MustCompile("^[0-9a-f]{64}$")
	if !validator.MatchString(hexKey) {
		return []byte{}, fmt.Errorf("key was not a hex string representing 32 bytes, must match [0-9a-f]{64}")
	}

	key, _ := hex.DecodeString(hexKey)

	return key, nil
}

func parseKeyFromConfiguration(config cryptoconfig.Config) ([]byte, error) {
	hexkey, ok := config.Parameters["key"]
	if !ok {
		return []byte{}, fmt.Errorf("configuration for AES256 needs the parameter 'key' set to a 32 byte lower case hexadecimal value")
	}

	key, err := parseKey(hexkey)
	if err != nil {
		return []byte{}, err
	}

	return key, nil
}

// determine if data (which is a []byte containing a json structure) is encrypted, that is, of the following form:
//
//	{"crypted":"<hex containing iv and payload>"}
func (a *AES256CFBMethod) isEncrypted(data []byte) bool {
	validator := regexp.MustCompile(`^{"crypted":".*$`)
	return validator.Match(data)
}

func (a *AES256CFBMethod) isSyntacticallyValidEncrypted(data []byte) bool {
	validator := regexp.MustCompile(`^{"crypted":"[0-9a-f]+"}$`)
	return validator.Match(data)
}

func (a *AES256CFBMethod) decodeFromEncryptedJsonWithChecks(jsonCryptedData []byte) ([]byte, error) {
	if !a.isSyntacticallyValidEncrypted(jsonCryptedData) {
		return []byte{}, fmt.Errorf("ciphertext contains invalid characters, possibly cut off or garbled")
	}

	// extract the hex part only, cutting off {"crypted":" (12 characters) and "} (2 characters)
	src := jsonCryptedData[12 : len(jsonCryptedData)-2]

	ciphertext := make([]byte, hex.DecodedLen(len(src)))
	n, err := hex.Decode(ciphertext, src)
	if err != nil {
		return []byte{}, err
	}
	if n != hex.DecodedLen(len(src)) {
		return []byte{}, fmt.Errorf("did not fully decode, only read %d characters before encountering an error", n)
	}
	return ciphertext, nil
}

func (a *AES256CFBMethod) encodeToEncryptedJson(ciphertext []byte) []byte {
	prefix := []byte(`{"crypted":"`)
	postfix := []byte(`"}`)
	encryptedHex := make([]byte, hex.EncodedLen(len(ciphertext)))
	_ = hex.Encode(encryptedHex, ciphertext)

	return append(append(prefix, encryptedHex...), postfix...)
}

func (a *AES256CFBMethod) attemptDecryption(jsonCryptedData []byte, key []byte) ([]byte, error) {
	ciphertext, err := a.decodeFromEncryptedJsonWithChecks(jsonCryptedData)
	if err != nil {
		return []byte{}, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return []byte{}, err
	}

	if len(ciphertext) < aes.BlockSize {
		return []byte{}, fmt.Errorf("ciphertext too short, did not contain initial vector")
	}
	iv := ciphertext[:aes.BlockSize]
	payloadWithHash := ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)

	// XORKeyStream can work in-place if the two arguments are the same.
	stream.XORKeyStream(payloadWithHash, payloadWithHash)

	plaintextPayload := payloadWithHash[:len(payloadWithHash)-sha256.Size]
	hashRead := payloadWithHash[len(payloadWithHash)-sha256.Size:]

	hashComputed := sha256.Sum256(plaintextPayload)
	for i, v := range hashComputed {
		if v != hashRead[i] {
			return []byte{}, fmt.Errorf("hash of decrypted payload did not match at position %d", i)
		}
	}

	// payloadWithHash is now decrypted
	return plaintextPayload, nil
}

func (a *AES256CFBMethod) attemptEncryption(plaintextPayload []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return []byte{}, err
	}

	ciphertext := make([]byte, aes.BlockSize+len(plaintextPayload)+sha256.Size)
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return []byte{}, err
	}

	// add hash over plaintext to end of plaintext (allows integrity check when decrypting)
	hashArray := sha256.Sum256(plaintextPayload)
	plaintextWithHash := append(plaintextPayload, hashArray[0:sha256.Size]...)

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintextWithHash)

	return a.encodeToEncryptedJson(ciphertext), nil
}

// Encrypt data (which is a []byte containing a json structure) into a json structure
//
//	{"crypted":"<hex-encoded random iv + hex-encoded CFB encrypted data including hash>"}
//
// fail if encryption is not possible to prevent writing unencrypted state
func (a *AES256CFBMethod) Encrypt(plaintextPayload []byte, config cryptoconfig.Config) ([]byte, cryptoconfig.Config, error) {
	key, err := parseKeyFromConfiguration(config)
	if err != nil {
		return []byte{}, config, err
	}

	encrypted, err := a.attemptEncryption(plaintextPayload, key)
	if err != nil {
		return []byte{}, config, err
	}
	return encrypted, config, nil
}

// Decrypt the hex-encoded contents of data, which is expected to be of the form
//
//	{"crypted":"<hex containing iv and payload>"}
//
// supports reading unencrypted state as well but logs a warning
func (a *AES256CFBMethod) Decrypt(data []byte, config cryptoconfig.Config) ([]byte, cryptoconfig.Config, error) {
	if a.isEncrypted(data) {
		key, err := parseKeyFromConfiguration(config)
		if err != nil {
			return []byte{}, config, err
		}

		candidate, err := a.attemptDecryption(data, key)
		if err != nil {
			return []byte{}, config, err
		}
		return candidate, config, nil
	} else {
		log.Printf("[WARN] found unencrypted state, transparently reading it anyway")
		return data, config, nil
	}
}
