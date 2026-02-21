package bufpool

import (
	"sync"
)

const (
	// TUNMTU максимальный размер передаваемой единицы (MTU)
	TUNMTU = 1500
	// HeaderSize размер заголовка протокола (5 байт: 4 байта для размера + 1 байт флаги)
	HeaderSize = 5
	// NonceSize размер nonce для ChaCha20-Poly1305 (12 байт)
	NonceSize = 12
	// Overhead размер дополнительных данных (nonce + tag)
	Overhead = NonceSize + 16 // 12 байт nonce + 16 байт tag
)

var (
	// PacketPool пул для пакетов размером TUNMTU
	PacketPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, TUNMTU)
		},
	}

	// EncryptedPacketPool пул для зашифрованных пакетов (MTU + overhead)
	EncryptedPacketPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, TUNMTU+Overhead)
		},
	}

	// HeaderPool пул для заголовков протокола
	HeaderPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, HeaderSize)
		},
	}

	// NoncePool пул для nonce значений
	NoncePool = sync.Pool{
		New: func() interface{} {
			return make([]byte, NonceSize)
		},
	}
)

// GetPacket получает буфер из пула пакетов
func GetPacket() []byte {
	return PacketPool.Get().([]byte)
}

// PutPacket возвращает буфер в пул пакетов
func PutPacket(buf []byte) {
	if cap(buf) >= TUNMTU {
		PacketPool.Put(buf[:TUNMTU])
	}
}

// GetEncryptedPacket получает буфер для зашифрованного пакета
func GetEncryptedPacket() []byte {
	return EncryptedPacketPool.Get().([]byte)
}

// PutEncryptedPacket возвращает буфер зашифрованного пакета в пул
func PutEncryptedPacket(buf []byte) {
	if cap(buf) >= TUNMTU+Overhead {
		EncryptedPacketPool.Put(buf[:TUNMTU+Overhead])
	}
}

// GetHeader получает буфер заголовка из пула
func GetHeader() []byte {
	return HeaderPool.Get().([]byte)
}

// PutHeader возвращает буфер заголовка в пул
func PutHeader(buf []byte) {
	if cap(buf) >= HeaderSize {
		HeaderPool.Put(buf[:HeaderSize])
	}
}

// GetNonce получает буфер nonce из пула
func GetNonce() []byte {
	return NoncePool.Get().([]byte)
}

// PutNonce возвращает буфер nonce в пул
func PutNonce(buf []byte) {
	if cap(buf) >= NonceSize {
		NoncePool.Put(buf[:NonceSize])
	}
}
