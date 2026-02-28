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
	MaxPacketSize = 1500 - 20 - 8 - HeaderSize - 1
)

// Crypto interface for encrypting and decrypting packets with AAD
type Crypto interface {
	Encrypt(plaintext []byte, aad []byte) ([]byte, error)
	Decrypt(ciphertext []byte, aad []byte) ([]byte, error)
}

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
	crypto     Crypto
	replay     *AntiReplayWindow

	// SOCKS5 Поддержка
	isSocks5     bool
	socks5Conn   net.Conn       // TCP соединение для контроля SOCKS5 (должно жить)
	socks5UDP    *net.UDPAddr   // Реальный адрес куда нужно слать UDP данные Xray 
	socks5Remote *net.UDPAddr   // Конечный адрес VPN сервера куда Xray должен переслать пакет
}

// NewUDPTransport создает новый UDP транспорт с поддержкой опционального SOCKS5 прокси
func NewUDPTransport(localAddr, remoteAddr string, keepaliveInterval time.Duration, crypto Crypto, socks5Proxy string) (*UDPTransport, error) {
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
		crypto:     crypto,
		replay:     NewAntiReplayWindow(0),
	}

	// Настройка SOCKS5 UDP Associate
	if socks5Proxy != "" {
		if remote == nil {
			return nil, fmt.Errorf("remote address must be explicitly set when using SOCKS5")
		}
		transport.isSocks5 = true
		transport.socks5Remote = remote

		// 1. Подключаемся к SOCKS5 по TCP
		socksConn, err := net.DialTimeout("tcp", socks5Proxy, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("socks5 proxy dial failed: %w", err)
		}
		transport.socks5Conn = socksConn

		// 2. Отправляем SOCKS5 Handshake (Version 5, 1 Method: No Auth)
		if _, err := socksConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return nil, fmt.Errorf("socks5 handshake failed: %w", err)
		}
		
		response := make([]byte, 2)
		if _, err := socksConn.Read(response); err != nil || response[1] != 0x00 {
			return nil, fmt.Errorf("socks5 auth negotiation failed: %v", err)
		}

		// 3. Отправляем запрос UDP Associate
		// cmd=0x03 (UDP Associate), rsv=0x00, atyp=0x01 (IPv4), dst.addr=0.0.0.0, dst.port=0
		udpAssocReq := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
		if _, err := socksConn.Write(udpAssocReq); err != nil {
			return nil, fmt.Errorf("socks5 UDP associate request failed: %w", err)
		}

		// 4. Читаем ответ сокета (где Xray открыл UDP порт для нас)
		assocResp := make([]byte, 10)
		if _, err := socksConn.Read(assocResp); err != nil || assocResp[1] != 0x00 {
			return nil, fmt.Errorf("socks5 UDP associate rejected: %v", err)
		}

		// Парсим выданый нам IP:PORT прокси-сервера для отправки UDP
		bndIP := net.IPv4(assocResp[4], assocResp[5], assocResp[6], assocResp[7])
		bndPort := int(binary.BigEndian.Uint16(assocResp[8:10]))
		
		transport.socks5UDP = &net.UDPAddr{
			IP:   bndIP,
			Port: bndPort,
		}

		// Если Xray вернул 0.0.0.0, шлем на тот же IP, что и TCP прокси
		if bndIP.IsUnspecified() {
			proxyHost, _, _ := net.SplitHostPort(socks5Proxy)
			transport.socks5UDP.IP = net.ParseIP(proxyHost)
		}
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

// Write отправляет данные через UDP (предварительно зашифровав их вместе с AAD флагом сжатия)
// isCompressed передается в AAD для защиты заголовков
func (t *UDPTransport) Write(data []byte, isCompressed bool) (int, error) {
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

	// Формируем AAD (6 байт): тип (1) + sequence (4) + compressFlag (1)
	aad := make([]byte, HeaderSize+1)
	aad[0] = PacketTypeData
	binary.BigEndian.PutUint32(aad[1:5], seq)
	if isCompressed {
		aad[5] = 0x01
	} else {
		aad[5] = 0x00
	}

	encrypted, err := t.crypto.Encrypt(data, aad)
	if err != nil {
		return 0, err
	}

	// Собираем финальный пакет: AAD + encrypted
	packet := make([]byte, len(aad)+len(encrypted))
	copy(packet[:len(aad)], aad)
	copy(packet[len(aad):], encrypted)

	var n int
	
	if t.isSocks5 {
		// SOCKS5 UDP пакет требует префикс
		// +-----+------+------+----------+----------+----------+
		// | RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
		// +-----+------+------+----------+----------+----------+
		// |  2  |   1  |   1  | Variable |     2    | Variable |
		// +-----+------+------+----------+----------+----------+
		socksHeader := make([]byte, 10)
		socksHeader[0] = 0x00 // RSV
		socksHeader[1] = 0x00 // RSV
		socksHeader[2] = 0x00 // FRAG
		socksHeader[3] = 0x01 // ATYP (IPv4)
		copy(socksHeader[4:8], t.socks5Remote.IP.To4())
		binary.BigEndian.PutUint16(socksHeader[8:10], uint16(t.socks5Remote.Port))

		fullPacket := append(socksHeader, packet...)
		n, err = t.conn.WriteToUDP(fullPacket, t.socks5UDP)
		// Корректируем длину для логики возврата
		if err == nil {
			n -= 10
		}
	} else {
		n, err = t.conn.WriteToUDP(packet, t.remoteAddr)
	}

	if err != nil {
		return 0, err
	}

	if n > len(aad) {
		return len(data), nil
	}
	return 0, nil
}

// Read читает данные из UDP и расшифровывает
// Возвращает (расшифрованные_данные, флаг_сжатия, caller_addr, error)
func (t *UDPTransport) Read(data []byte) (int, bool, *net.UDPAddr, error) {
	buf := make([]byte, MaxPacketSize+HeaderSize+100+10) // +100 MAC, +10 SOCKS5
	n, addr, err := t.conn.ReadFromUDP(buf)
	if err != nil {
		return 0, false, addr, err
	}

	// Снятие SOCKS5 заголовка с входящего UDP пакета
	offset := 0
	if t.isSocks5 {
		if n < 10 {
			return 0, false, addr, fmt.Errorf("truncated SOCKS5 UDP packet")
		}
		// Пропускаем RSV(2), FRAG(1)
		atyp := buf[3]
		if atyp == 0x01 { // IPv4
			offset = 10
		} else if atyp == 0x03 { // Domain
			domainLen := int(buf[4])
			offset = 5 + domainLen + 2
		} else if atyp == 0x04 { // IPv6
			offset = 22
		} else {
			return 0, false, addr, fmt.Errorf("unsupported SOCKS5 atyp: %d", atyp)
		}
		
		if n < offset {
			return 0, false, addr, fmt.Errorf("truncated SOCKS5 UDP payload")
		}
		
		buf = buf[offset:]
		n -= offset
		addr = t.socks5Remote // Подменяем отправителя на целевой VPN сервер
	}

	// Если удаленный адрес еще не установлен, устанавливаем его (кроме случаев когда это сервер и клиент новый)
	// В сервере мы не можем менять remoteAddr на лету так просто, поэтому это ок только для клиента
	if t.remoteAddr == nil {
		t.remoteAddr = addr
		// Запускаем keepalive после установки адреса
		if t.keepalive > 0 {
			t.wg.Add(1)
			go t.keepaliveLoop()
		}
	}

	if n < HeaderSize {
		return 0, false, addr, fmt.Errorf("packet too short")
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
		return 0, false, addr, nil // Не возвращаем данные для keepalive
	}

	if packetType == PacketTypeKeepaliveAck {
		return 0, false, addr, nil // Игнорируем ACK
	}

	if packetType != PacketTypeData {
		return 0, false, addr, fmt.Errorf("unknown packet type: %d", packetType)
	}

	if n < HeaderSize+1 {
		return 0, false, addr, fmt.Errorf("packet too short for compression flag")
	}

	// Проверяем Anti-Replay окно
	if !t.replay.Check(seq) {
		return 0, false, addr, fmt.Errorf("replay attack detected, seq: %d", seq)
	}

	aad := buf[:HeaderSize+1]
	isCompressed := aad[5] == 0x01
	encrypted := buf[HeaderSize+1 : n]

	decrypted, err := t.crypto.Decrypt(encrypted, aad)
	if err != nil {
		return 0, false, addr, err
	}

	if len(decrypted) > len(data) {
		return 0, false, addr, fmt.Errorf("buffer too small: need %d bytes", len(decrypted))
	}

	copy(data, decrypted)
	return len(decrypted), isCompressed, addr, nil
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

			if t.isSocks5 {
				// Упаковка в SOCKS5 заголовок
				socksHeader := make([]byte, 10)
				socksHeader[3] = 0x01
				copy(socksHeader[4:8], t.socks5Remote.IP.To4())
				binary.BigEndian.PutUint16(socksHeader[8:10], uint16(t.socks5Remote.Port))
				fullPacket := append(socksHeader, packet...)
				t.conn.WriteToUDP(fullPacket, t.socks5UDP)
			} else {
				t.conn.WriteToUDP(packet, t.remoteAddr)
			}
		}
	}
}

// Close закрывает транспорт
func (t *UDPTransport) Close() error {
	select {
	case <-t.done:
		// уже закрыт
	default:
		close(t.done)
	}

	if t.socks5Conn != nil {
		t.socks5Conn.Close()
	}

	t.wg.Wait()
	return t.conn.Close()
}

// Conn возвращает UDP соединение для использования в других местах
func (t *UDPTransport) Conn() *net.UDPConn {
	return t.conn
}
