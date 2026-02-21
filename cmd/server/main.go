package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"myvpn/server"
)

func main() {
	var (
		listenAddr   = flag.String("addr", ":8080", "Address to listen on")
		keyFile      = flag.String("key", "", "Path to encryption key file (32 bytes). If not provided, a random key will be generated")
		verbose      = flag.Bool("verbose", false, "Enable verbose logging (logs every packet)")
		pprofAddr    = flag.String("pprof", ":6060", "Address for pprof HTTP server (empty to disable)")
		metricsAddr  = flag.String("metrics", ":6061", "Address for metrics HTTP server (empty to disable)")
	)
	flag.Parse()

	// Загружаем или генерируем ключ
	key, err := loadOrGenerateKey(*keyFile)
	if err != nil {
		log.Fatalf("Failed to load/generate key: %v", err)
	}

	// Создаем сервер
	srv, err := server.NewServer(*listenAddr, key, *verbose)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Запускаем сервер
	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Запускаем pprof сервер если указан адрес
	if *pprofAddr != "" {
		go func() {
			log.Printf("Starting pprof server on %s", *pprofAddr)
			log.Println(http.ListenAndServe(*pprofAddr, nil))
		}()
	}

	// Запускаем метрики сервер если указан адрес
	if *metricsAddr != "" {
		go startMetricsServer(*metricsAddr)
	}

	// Обрабатываем сигналы для корректного завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Println("VPN server started. Press Ctrl+C to stop.")
	<-sigChan

	log.Println("Shutting down server...")
	if err := srv.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}

	log.Println("Server stopped.")
}

// loadOrGenerateKey загружает ключ из файла или генерирует новый
func loadOrGenerateKey(keyFile string) ([]byte, error) {
	const keySize = 32 // 32 байта для ChaCha20-Poly1305

	if keyFile != "" {
		// Загружаем ключ из файла
		key, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read key file: %w", err)
		}

		if len(key) != keySize {
			return nil, fmt.Errorf("invalid key size: expected %d bytes, got %d", keySize, len(key))
		}

		return key, nil
	}

	// Генерируем случайный ключ
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	log.Println("Generated random encryption key. Save it for client configuration!")
	log.Printf("Key (hex): %x\n", key)

	return key, nil
}

// startMetricsServer запускает HTTP сервер для метрик
func startMetricsServer(addr string) {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "# VPN Server Metrics\n")
		fmt.Fprintf(w, "# Time: %s\n\n", time.Now().Format(time.RFC3339))
		// Здесь можно добавить экспорт метрик в формате Prometheus или просто текстовый формат
		fmt.Fprintf(w, "metrics_endpoint_active 1\n")
	})

	log.Printf("Starting metrics server on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("Metrics server error: %v", err)
	}
}
