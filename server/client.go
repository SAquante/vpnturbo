package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
	"myvpn/internal"
	"myvpn/internal/compress"
	"myvpn/internal/transport"
)

// Client представляет клиентское соединение (UDP)
type Client struct {
	remoteAddr *net.UDPAddr
	crypto     *internal.Crypto
	tun        *TUN
	done       chan struct{}
	wg         sync.WaitGroup
	verbose    bool
}

// NewClient создает новый клиент для UDP
func NewClient(remoteAddr *net.UDPAddr, crypto *internal.Crypto, tun *TUN, verbose bool) *Client {
	return &Client{
		remoteAddr: remoteAddr,
		crypto:     crypto,
		tun:        tun,
		done:       make(chan struct{}),
		verbose:    verbose,
	}
}

// Handle обрабатывает клиентское соединение (для UDP это просто маркер)
func (c *Client) Handle() error {
	log.Printf("New client connected from %s", c.remoteAddr)
	// Для UDP клиенты обрабатываются централизованно в сервере
	return nil
}

// SendPacket отправляет пакет клиенту через UDP транспорт
func (c *Client) SendPacket(transport *transport.UDPTransport, packet []byte) error {
	// Сжимаем пакет (опционально)
	compressed, isCompressed, err := compress.Compress(packet)
	if err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	// Шифруем пакет
	encrypted, err := c.crypto.Encrypt(compressed)
	if err != nil {
		return err
	}

	// Добавляем флаг сжатия в начало зашифрованных данных
	result := make([]byte, 1+len(encrypted))
	if isCompressed {
		result[0] = internal.FlagCompressed
	} else {
		result[0] = 0
	}
	copy(result[1:], encrypted)

	// Устанавливаем удаленный адрес и отправляем
	transport.SetRemoteAddr(c.remoteAddr)
	_, err = transport.Write(result)
	return err
}


// Close закрывает клиентское соединение
func (c *Client) Close() error {
	select {
	case <-c.done:
		// Уже закрыто
		return nil
	default:
		close(c.done)
		return nil
	}
}

// Server представляет VPN сервер
type Server struct {
	listenAddr     string
	tun            *TUN
	crypto         *internal.Crypto
	transport      *transport.UDPTransport
	networkManager *NetworkManager
	clients        map[string]*Client
	clientsMu      sync.RWMutex
	done           chan struct{}
	wg             sync.WaitGroup
	verbose        bool
}

// NewServer создает новый VPN сервер
func NewServer(listenAddr string, key []byte, verbose bool) (*Server, error) {
	// Создаем TUN интерфейс
	tun, err := NewTUN(TUNInterfaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN interface: %w", err)
	}

	// Создаем криптографию
	crypto, err := internal.NewCrypto(key)
	if err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to create crypto: %w", err)
	}

	// Создаем менеджер сетевых настроек
	networkManager, err := NewNetworkManager(TUNInterfaceName)
	if err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to create network manager: %w", err)
	}

	return &Server{
		listenAddr:     listenAddr,
		tun:            tun,
		crypto:         crypto,
		networkManager: networkManager,
		clients:        make(map[string]*Client),
		done:           make(chan struct{}),
		verbose:        verbose,
	}, nil
}

// Start запускает сервер
func (s *Server) Start() error {
	// Настраиваем сеть (IP forwarding, NAT, firewall)
	if err := s.networkManager.Setup(); err != nil {
		return fmt.Errorf("failed to setup network: %w", err)
	}

	// Создаем UDP транспорт
	udpTransport, err := transport.NewUDPTransport(s.listenAddr, "", 30*time.Second)
	if err != nil {
		s.networkManager.Cleanup()
		return fmt.Errorf("failed to create UDP transport: %w", err)
	}

	s.transport = udpTransport
	log.Printf("VPN server listening on %s (UDP)", s.listenAddr)
	log.Printf("TUN interface: %s", s.tun.Name())

	// Запускаем горутину для чтения из TUN
	s.wg.Add(1)
	go s.handleTunToClients()

	// Запускаем горутину для чтения от клиентов
	s.wg.Add(1)
	go s.handleClientsToTun()

	return nil
}

// handleTunToClients читает пакеты из TUN и отправляет всем клиентам
func (s *Server) handleTunToClients() {
	defer s.wg.Done()

	packet := make([]byte, TUNMTU)

	for {
		select {
		case <-s.done:
			return
		default:
		}

		n, err := s.tun.Read(packet)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				if err != io.EOF {
					log.Printf("Error reading from TUN: %v", err)
				}
				continue
			}
		}

		if n > 0 {
			// Отправляем всем подключенным клиентам
			s.clientsMu.RLock()
			clientCount := len(s.clients)
			if s.verbose {
				log.Printf("Read %d bytes from TUN, sending to %d client(s)", n, clientCount)
			}
			if clientCount == 0 {
				s.clientsMu.RUnlock()
				log.Printf("Warning: no clients to send TUN packet to (dropped %d bytes)", n)
				continue
			}
			for _, client := range s.clients {
				if err := client.SendPacket(s.transport, packet[:n]); err != nil {
					if s.verbose {
						log.Printf("Error sending packet to client %s: %v", client.remoteAddr, err)
					}
				}
			}
			s.clientsMu.RUnlock()
		}
	}
}

// handleClientsToTun читает пакеты от клиентов и записывает в TUN
func (s *Server) handleClientsToTun() {
	defer s.wg.Done()

	// MaxPacketSize в транспорте = 1467 байт (это максимальный размер данных без UDP заголовка)
	buf := make([]byte, transport.MaxPacketSize)

	for {
		select {
		case <-s.done:
			return
		default:
			n, err := s.transport.Read(buf)
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					log.Printf("Error reading from UDP: %v", err)
					continue
				}
			}

			if n > 0 {
				// Получаем адрес клиента
				remoteAddr := s.transport.RemoteAddr()
				if remoteAddr == nil {
					continue
				}

				// Регистрируем клиента если его еще нет
				clientKey := remoteAddr.String()
				s.clientsMu.Lock()
				client, exists := s.clients[clientKey]
				if !exists {
					client = NewClient(remoteAddr, s.crypto, s.tun, s.verbose)
					s.clients[clientKey] = client
					log.Printf("New client connected from %s", remoteAddr)
				}
				s.clientsMu.Unlock()

				if n < 1 {
					continue
				}

				// Извлекаем флаг сжатия
				flags := buf[0]
				isCompressed := (flags & internal.FlagCompressed) != 0

				// Дешифруем пакет
				encrypted := buf[1:n]
				packet, err := s.crypto.Decrypt(encrypted)
				if err != nil {
					log.Printf("Error decrypting packet from %s: %v", remoteAddr, err)
					continue
				}

				// Распаковываем если нужно
				if isCompressed {
					packet, err = compress.Decompress(packet, true)
					if err != nil {
						log.Printf("Error decompressing packet from %s: %v", remoteAddr, err)
						continue
					}
				}

				if len(packet) > 0 {
					if s.verbose {
						log.Printf("Received %d bytes from client %s, writing to TUN", len(packet), remoteAddr)
					}
					// Записываем пакет в TUN
					if _, err := s.tun.Write(packet); err != nil {
						log.Printf("Error writing packet to TUN: %v", err)
					}
				}
			}
		}
	}
}

// Stop останавливает сервер
func (s *Server) Stop() error {
	close(s.done)

	var errs []error

	if s.transport != nil {
		if err := s.transport.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	s.wg.Wait()

	// Восстанавливаем сетевые настройки
	if s.networkManager != nil {
		if err := s.networkManager.Cleanup(); err != nil {
			errs = append(errs, err)
		}
	}

	if s.tun != nil {
		if err := s.tun.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping server: %v", errs)
	}

	return nil
}
