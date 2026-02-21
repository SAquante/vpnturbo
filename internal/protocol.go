package internal

import (
	"encoding/binary"
	"errors"
	"io"

	"myvpn/internal/bufpool"
)

// Protocol управляет протоколом обмена данными между клиентом и сервером
type Protocol struct {
	crypto *Crypto
}

func NewProtocol(crypto *Crypto) *Protocol {
	return &Protocol{crypto: crypto}
}

// SendPacket отправляет зашифрованный пакет через writer
func (p *Protocol) SendPacket(writer io.Writer, packet []byte) error {
	// Шифруем пакет
	encrypted, err := p.crypto.Encrypt(packet)
	if err != nil {
		return err
	}

	// Получаем буфер заголовка из пула
	sizeBuf := bufpool.GetHeader()
	defer bufpool.PutHeader(sizeBuf)

	binary.BigEndian.PutUint32(sizeBuf, uint32(len(encrypted)))

	if _, err := writer.Write(sizeBuf); err != nil {
		return err
	}

	if _, err := writer.Write(encrypted); err != nil {
		return err
	}

	return nil
}

// ReceivePacket получает и дешифрует пакет из reader
func (p *Protocol) ReceivePacket(reader io.Reader) ([]byte, error) {
	// Получаем буфер заголовка из пула
	sizeBuf := bufpool.GetHeader()
	defer bufpool.PutHeader(sizeBuf)

	if _, err := io.ReadFull(reader, sizeBuf); err != nil {
		return nil, err
	}

	size := binary.BigEndian.Uint32(sizeBuf)

	if size > MaxPacketSize {
		return nil, errors.New("packet size too large")
	}

	if size == 0 {
		return nil, errors.New("zero packet size")
	}

	// Для зашифрованных данных используем пул если размер подходит
	var encrypted []byte
	if size <= TUNMTU+Overhead {
		buf := bufpool.GetEncryptedPacket()
		encrypted = buf[:size]
		defer func() {
			bufpool.PutEncryptedPacket(buf)
		}()
	} else {
		encrypted = make([]byte, size)
	}

	if _, err := io.ReadFull(reader, encrypted); err != nil {
		return nil, err
	}

	packet, err := p.crypto.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}

	return packet, nil
}
