package transport

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"sync"
	"time"
	"golang.org/x/sys/unix"
)

const (
	// PacketTypeData обычный пакет данных
	PacketTypeData = 0x01
	// PacketTypeKeepalive keepalive пакет
	PacketTypeKeepalive = 0x02
	// PacketTypeKeepaliveAck ответ на keepalive
	PacketTypeKeepaliveAck = 0x03

	// HeaderSize размер заголовка UDP пакета (1 байт тип + 4 байта sequence)
	HeaderSize = 5
	// CompressionFlagSize размер флага сжатия (1 байт)
	CompressionFlagSize = 1
	// MaxPacketSize максимальный размер UDP пакета (MTU 1500 - IP header 20 - UDP header 8 - наш header 5)
	// Это максимальный размер данных которые можно отправить через Write() до добавления UDP заголовка
	// Флаг сжатия уже включен в данные, передаваемые в Write()
	MaxPacketSize = 1500 - 20 - 8 - HeaderSize
)

// UDPTransport представляет UDP транспорт для VPN
type UDPTransport struct {
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	localAddr  *net.UDPAddr
	sequence   uint32
	seqMutex   sync.Mutex
	keepalive  time.Duration
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewUDPTransport создает новый UDP транспорт
func NewUDPTransport(localAddr, remoteAddr string, keepaliveInterval time.Duration) (*UDPTransport, error) {
	local, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local address: %w", err)
	}

	var remote *net.UDPAddr
	if remoteAddr != "" {
		remote, err = net.ResolveUDPAddr("udp", remoteAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve remote address: %w", err)
		}
	}

	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		return nil, fmt.Errorf("failed to listen UDP: %w", err)
	}

	// Оптимизация UDP сокета
	if err := setUDPOptions(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set UDP options: %w", err)
	}

	transport := &UDPTransport{
		conn:       conn,
		remoteAddr: remote,
		localAddr:  local,
		keepalive:  keepaliveInterval,
		done:       make(chan struct{}),
	}

	// Запускаем keepalive если указан удаленный адрес
	if remote != nil && keepaliveInterval > 0 {
		transport.wg.Add(1)
		go transport.keepaliveLoop()
	}

	return transport, nil
}

// setUDPOptions настраивает UDP сокет для оптимизации производительности
func setUDPOptions(conn *net.UDPConn) error {
	file, err := conn.File()
	if err != nil {
		return err
	}
	defer file.Close()

	fd := int(file.Fd())

	// Увеличиваем буферы приема и отправки
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, unix.SO_RCVBUF, 4*1024*1024); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, unix.SO_SNDBUF, 4*1024*1024); err != nil {
		return err
	}

	// Включаем reuse port для балансировки нагрузки (если поддерживается)
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)

	return nil
}

// Write отправляет данные через UDP
func (t *UDPTransport) Write(data []byte) (int, error) {
	if t.remoteAddr == nil {
		return 0, fmt.Errorf("remote address not set")
	}

	if len(data) > MaxPacketSize {
		return 0, fmt.Errorf("packet too large: %d bytes (max %d)", len(data), MaxPacketSize)
	}

	// Получаем sequence number
	t.seqMutex.Lock()
	seq := t.sequence
	t.sequence++
	t.seqMutex.Unlock()

	// Формируем пакет: тип (1 байт) + sequence (4 байта) + данные
	packet := make([]byte, HeaderSize+len(data))
	packet[0] = PacketTypeData
	binary.BigEndian.PutUint32(packet[1:5], seq)
	copy(packet[HeaderSize:], data)

	n, err := t.conn.WriteToUDP(packet, t.remoteAddr)
	if err != nil {
		return 0, err
	}

	// Возвращаем размер данных без заголовка
	if n > HeaderSize {
		return n - HeaderSize, nil
	}
	return 0, nil
}

// Read читает данные из UDP
func (t *UDPTransport) Read(data []byte) (int, error) {
	buf := make([]byte, MaxPacketSize+HeaderSize)
	n, addr, err := t.conn.ReadFromUDP(buf)
	if err != nil {
		return 0, err
	}

	// Если удаленный адрес еще не установлен, устанавливаем его
	if t.remoteAddr == nil {
		t.remoteAddr = addr
		// Запускаем keepalive после установки адреса
		if t.keepalive > 0 {
			t.wg.Add(1)
			go t.keepaliveLoop()
		}
	}

	if n < HeaderSize {
		return 0, fmt.Errorf("packet too short")
	}

	packetType := buf[0]
	seq := binary.BigEndian.Uint32(buf[1:5])

	// Обрабатываем keepalive пакеты
	if packetType == PacketTypeKeepalive {
		// Отправляем ACK
		ack := make([]byte, HeaderSize)
		ack[0] = PacketTypeKeepaliveAck
		binary.BigEndian.PutUint32(ack[1:5], seq)
		t.conn.WriteToUDP(ack, addr)
		return 0, nil // Не возвращаем данные для keepalive
	}

	if packetType == PacketTypeKeepaliveAck {
		return 0, nil // Игнорируем ACK
	}

	if packetType != PacketTypeData {
		return 0, fmt.Errorf("unknown packet type: %d", packetType)
	}

	// Копируем данные
	dataLen := n - HeaderSize
	if dataLen > len(data) {
		return 0, fmt.Errorf("buffer too small: need %d bytes", dataLen)
	}

	copy(data, buf[HeaderSize:n])
	return dataLen, nil
}

// SetRemoteAddr устанавливает удаленный адрес
func (t *UDPTransport) SetRemoteAddr(addr *net.UDPAddr) {
	t.remoteAddr = addr
	if t.keepalive > 0 {
		t.wg.Add(1)
		go t.keepaliveLoop()
	}
}

// RemoteAddr возвращает удаленный адрес
func (t *UDPTransport) RemoteAddr() *net.UDPAddr {
	return t.remoteAddr
}

// LocalAddr возвращает локальный адрес
func (t *UDPTransport) LocalAddr() *net.UDPAddr {
	return t.localAddr
}

// keepaliveLoop отправляет keepalive пакеты
func (t *UDPTransport) keepaliveLoop() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.keepalive)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			if t.remoteAddr == nil {
				continue
			}

			t.seqMutex.Lock()
			seq := t.sequence
			t.sequence++
			t.seqMutex.Unlock()

			packet := make([]byte, HeaderSize)
			packet[0] = PacketTypeKeepalive
			binary.BigEndian.PutUint32(packet[1:5], seq)

			t.conn.WriteToUDP(packet, t.remoteAddr)
		}
	}
}

// Close закрывает транспорт
func (t *UDPTransport) Close() error {
	select {
	case <-t.done:
		return nil
	default:
		close(t.done)
	}

	t.wg.Wait()
	return t.conn.Close()
}

// Conn возвращает UDP соединение для использования в других местах
func (t *UDPTransport) Conn() *net.UDPConn {
	return t.conn
}
