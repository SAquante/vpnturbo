package internal

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"golang.org/x/crypto/chacha20poly1305"
	"myvpn/internal/bufpool"
)

const (
	// KeySize размер ключа для ChaCha20-Poly1305 (32 байта)
	KeySize = chacha20poly1305.KeySize
	// NonceSize размер nonce для ChaCha20-Poly1305 (12 байт)
	NonceSize = chacha20poly1305.NonceSize
	// Overhead размер дополнительных данных (nonce + tag)
	Overhead = NonceSize + 16 // 12 байт nonce + 16 байт tag
	// MaxPacketSize максимальный размер пакета (MTU + overhead)
	MaxPacketSize = TUNMTU + Overhead
)

// Crypto управляет шифрованием и дешифрованием пакетов
type Crypto struct {
	aead cipher.AEAD
}

// NewCrypto создает новый экземпляр Crypto с заданным ключом
func NewCrypto(key []byte) (*Crypto, error) {
	if len(key) != KeySize {
		return nil, errors.New("invalid key size")
	}

	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}

	return &Crypto{aead: aead}, nil
}

// Encrypt шифрует данные и возвращает nonce + зашифрованные данные + tag
func (c *Crypto) Encrypt(plaintext []byte, aad []byte) ([]byte, error) {
	// Получаем nonce из пула
	nonce := bufpool.GetNonce()
	defer bufpool.PutNonce(nonce)

	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// Шифруем данные
	ciphertext := c.aead.Seal(nil, nonce, plaintext, aad)

	// Выделяем результат (не из пула, так как возвращаем его)
	result := make([]byte, NonceSize+len(ciphertext))
	copy(result[:NonceSize], nonce)
	copy(result[NonceSize:], ciphertext)

	return result, nil
}

// Decrypt дешифрует данные (nonce + encrypted_data + tag)
func (c *Crypto) Decrypt(ciphertext []byte, aad []byte) ([]byte, error) {
	if len(ciphertext) < Overhead {
		return nil, errors.New("ciphertext too short")
	}

	// Извлекаем nonce
	nonce := ciphertext[:NonceSize]
	encryptedData := ciphertext[NonceSize:]

	// Дешифруем данные
	plaintext, err := c.aead.Open(nil, nonce, encryptedData, aad)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
