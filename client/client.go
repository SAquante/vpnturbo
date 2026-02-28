package client

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"
	"myvpn/internal"
	"myvpn/internal/compress"
	"myvpn/internal/transport"
)

// VPNClient
type VPNClient struct {
	serverAddr   string
	tun          *TUN
	crypto       *internal.Crypto
	protocol     *internal.Protocol
	transport    *transport.UDPTransport
	socks5Proxy  string
	routeManager *RouteManager
	done         chan struct{}
	wg           sync.WaitGroup
	verbose      bool
	autoRoutes   bool
}

// NewVPNClient создает новый VPN клиент
func NewVPNClient(serverAddr string, key []byte, clientIP string, verbose bool, autoRoutes bool, socks5Proxy string) (*VPNClient, error) {
	// Создаем TUN интерфейс
	tun, err := NewTUN(TUNInterfaceName, clientIP)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN interface: %w", err)
	}

	// применяем шифрование
	crypto, err := internal.NewCrypto(key)
	if err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to create crypto: %w", err)
	}

	// Инициализируем протокол
	protocol := internal.NewProtocol(crypto)

	// Создаем менеджер маршрутов только если включена автоматическая настройка
	var routeManager *RouteManager
	if autoRoutes {
		routeManager, err = NewRouteManager(TUNInterfaceName, serverAddr)
		if err != nil {
			tun.Close()
			return nil, fmt.Errorf("failed to create route manager: %w", err)
		}
	}

	return &VPNClient{
		serverAddr:   serverAddr,
		tun:          tun,
		crypto:       crypto,
		protocol:     protocol,
		socks5Proxy:  socks5Proxy,
		routeManager: routeManager,
		done:         make(chan struct{}),
		verbose:      verbose,
		autoRoutes:   autoRoutes,
	}, nil
}

// Connect подключается к VPN серверу и начинает обмен пакетами
func (c *VPNClient) Connect() error {
	// Создаем UDP транспорт
	if c.socks5Proxy != "" {
		log.Printf("Connecting to %s via SOCKS5 proxy at %s", c.serverAddr, c.socks5Proxy)
	}
	udpTransport, err := transport.NewUDPTransport(":0", c.serverAddr, 30*time.Second, c.crypto, c.socks5Proxy)
	if err != nil {
		return fmt.Errorf("failed to create UDP transport: %w", err)
	}

	c.transport = udpTransport
	log.Printf("Connected to VPN server at %s", c.serverAddr)
	log.Printf("TUN interface: %s", c.tun.Name())

	// Настраиваем маршрутизацию всего трафика через VPN
	if c.autoRoutes && c.routeManager != nil {
		if err := c.routeManager.SetupRoutes(); err != nil {
			log.Printf("Warning: failed to setup routes: %v", err)
			log.Println("You may need to configure routes manually")
		} else {
			log.Println("✓ Routes configured: all traffic now goes through VPN")
		}
	}

	// Запускаем горутину для чтения из TUN и отправки на сервер
	c.wg.Add(1)
	go c.handleTunToServer()

	// Запускаем горутину для чтения от сервера и записи в TUN
	c.wg.Add(1)
	go c.handleServerToTun()

	// Ждем завершения
	c.wg.Wait()
	log.Println("Disconnected from VPN server")

	return nil
}

// handleTunToServer читает пакеты из TUN и отправляет на сервер
func (c *VPNClient) handleTunToServer() {
	defer c.wg.Done()

	packet := make([]byte, internal.TUNMTU)

	for {
		select {
		case <-c.done:
			log.Println("handleTunToServer: done signal received")
			return
		default:
		}

		// Устанавливаем deadline для возможности прерывания
		// Используем SetReadDeadline через файловый дескриптор TUN
		// Для TUN интерфейса используем прямое чтение с проверкой done канала
		// через неблокирующее чтение
		n, err := c.tun.Read(packet)
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				if err != io.EOF {
					log.Printf("Error reading from TUN: %v", err)
				} else {
					log.Println("TUN interface closed (EOF)")
				}
				c.Close()
				return
			}
		}

		if n > 0 {
			if c.verbose {
				log.Printf("Read %d bytes from TUN, sending to server", n)
			}
			// Отправляем пакет на сервер через UDP транспорт
			if err := c.sendPacketUDP(packet[:n]); err != nil {
				log.Printf("Error sending packet to server: %v", err)
				c.Close()
				return
			}
		}
	}
}

// sendPacketUDP отправляет пакет через UDP транспорт
func (c *VPNClient) sendPacketUDP(packet []byte) error {
	// Сжимаем пакет (опционально)
	compressed, isCompressed, err := compress.Compress(packet)
	if err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	// Отправляем через UDP транспорт, который сам зашифрует данные и добавит AAD заголовки
	_, err = c.transport.Write(compressed, isCompressed)
	return err
}

// handleServerToTun читает пакеты от сервера и записывает в TUN
func (c *VPNClient) handleServerToTun() {
	defer c.wg.Done()

	// Буфер должен быть достаточного размера для данных после шифрования + флаг сжатия
	// MaxPacketSize в транспорте = 1467 байт (это максимальный размер данных без UDP заголовка)
	buf := make([]byte, transport.MaxPacketSize)

	for {
		select {
		case <-c.done:
			log.Println("handleServerToTun: done signal received")
			return
		default:
			// Читаем из UDP транспорта
			n, isCompressed, _, err := c.transport.Read(buf)
			if err != nil {
				select {
				case <-c.done:
					return
				default:
					log.Printf("Error receiving packet from server: %v", err)
					c.Close()
					return
				}
			}

			if n > 0 {
				packet := buf[:n]

				// Распаковываем если нужно
				if isCompressed {
					packet, err = compress.Decompress(packet, true)
					if err != nil {
						log.Printf("Error decompressing packet: %v", err)
						continue
					}
				}

				if len(packet) > 0 {
					if c.verbose {
						log.Printf("Received %d bytes from server, writing to TUN", len(packet))
					}
					// Записываем пакет в TUN
					if _, err := c.tun.Write(packet); err != nil {
						log.Printf("Error writing packet to TUN: %v", err)
						c.Close()
						return
					}
				}
			}
		}
	}
}

// Close закрывает соединение и TUN интерфейс
func (c *VPNClient) Close() error {
	select {
	case <-c.done:
		// Уже закрыто
		return nil
	default:
		close(c.done)
	}

	var errs []error

	// Восстанавливаем старые маршруты
	if c.routeManager != nil {
		if err := c.routeManager.RestoreRoutes(); err != nil {
			log.Printf("Warning: failed to restore routes: %v", err)
			errs = append(errs, fmt.Errorf("failed to restore routes: %w", err))
		} else {
			log.Println("✓ Routes restored to original state")
		}
	}

	if c.transport != nil {
		if err := c.transport.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if c.tun != nil {
		if err := c.tun.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing client: %v", errs)
	}

	return nil
}
