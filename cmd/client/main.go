package main

import (
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"myvpn/client"
)

func main() {
	var (
		serverAddr      = flag.String("server", "", "VPN server address (e.g., 192.168.1.100:8080)")
		keyFile         = flag.String("key", "", "Path to encryption key file (32 bytes binary or 64 hex chars)")
		clientIP        = flag.String("ip", "10.0.0.2", "Client IP address for TUN interface")
		verbose         = flag.Bool("verbose", false, "Enable verbose logging (logs every packet)")
		pprofAddr       = flag.String("pprof", "127.0.0.1:6060", "Address for pprof HTTP server (empty to disable)")
		autoRoutes      = flag.Bool("auto-routes", true, "Automatically configure routes (redirect all traffic through VPN)")
		socks5Proxy     = flag.String("socks5", "", "SOCKS5 Proxy address for Xray-core backend (e.g., 127.0.0.1:1080)")
	)
	flag.Parse()

	if *serverAddr == "" {
		log.Fatal("Server address is required. Use -server flag")
	}

	if *keyFile == "" {
		log.Fatal("Key file is required. Use -key flag")
	}

	// Загружаем ключ
	keyData, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("Failed to read key file: %v", err)
	}

	// Определяем формат ключа и конвертируем при необходимости
	var key []byte
	const keySize = 32
	const hexKeySize = 64 // 32 байта в hex = 64 символа

	if len(keyData) == hexKeySize {
		// Попробуем декодировать как hex строку
		key, err = hex.DecodeString(string(keyData))
		if err != nil {
			log.Fatalf("Failed to decode hex key: %v", err)
		}
		if len(key) != keySize {
			log.Fatalf("Invalid hex key: decoded to %d bytes, expected %d", len(key), keySize)
		}
		log.Println("Key file detected as hex format, converted to binary")
	} else if len(keyData) == keySize {
		// Бинарный формат
		key = keyData
	} else {
		log.Fatalf("Invalid key size: expected %d bytes (binary) or %d chars (hex), got %d", keySize, hexKeySize, len(keyData))
	}

	// Создаем клиент
	vpnClient, err := client.NewVPNClient(*serverAddr, key, *clientIP, *verbose, *autoRoutes, *socks5Proxy)
	if err != nil {
		log.Fatalf("Failed to create VPN client: %v", err)
	}

	// Обрабатываем сигналы для корректного завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Запускаем pprof сервер если указан адрес
	if *pprofAddr != "" {
		go func() {
			log.Printf("Starting pprof server on %s", *pprofAddr)
			log.Println(http.ListenAndServe(*pprofAddr, nil))
		}()
	}

	// Запускаем подключение в отдельной горутине
	errChan := make(chan error, 1)
	go func() {
		if err := vpnClient.Connect(); err != nil {
			errChan <- err
		}
	}()

	log.Println("VPN client started. Press Ctrl+C to stop.")

	// Ждем сигнала или ошибки
	select {
	case <-sigChan:
		log.Println("Shutting down client...")
	case err := <-errChan:
		log.Printf("Connection error: %v", err)
	}

	if err := vpnClient.Close(); err != nil {
		log.Printf("Error closing client: %v", err)
	}

	log.Println("Client stopped.")
}
