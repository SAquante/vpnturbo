package compress

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

const (
	// CompressionThreshold минимальный размер данных для попытки сжатия
	CompressionThreshold = 64
	// CompressionRatioThreshold минимальный коэффициент сжатия для применения (0.9 = 10% сжатие)
	CompressionRatioThreshold = 0.9
)

// Compress сжимает данные используя LZ4, возвращает сжатые данные и флаг сжатия
func Compress(data []byte) ([]byte, bool, error) {
	if len(data) < CompressionThreshold {
		// Не сжимаем маленькие пакеты
		return data, false, nil
	}

	var buf bytes.Buffer
	writer := lz4.NewWriter(&buf)

	if _, err := writer.Write(data); err != nil {
		return nil, false, fmt.Errorf("failed to compress: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, false, fmt.Errorf("failed to close compressor: %w", err)
	}

	compressed := buf.Bytes()

	// Проверяем, действительно ли сжатие помогло
	ratio := float64(len(compressed)) / float64(len(data))
	if ratio >= CompressionRatioThreshold {
		// Сжатие не дало значительного эффекта
		return data, false, nil
	}

	return compressed, true, nil
}

// Decompress распаковывает данные используя LZ4
func Decompress(data []byte, compressed bool) ([]byte, error) {
	if !compressed {
		return data, nil
	}

	reader := lz4.NewReader(bytes.NewReader(data))

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}

	return buf.Bytes(), nil
}

// CompressWithPool сжимает данные используя пул буферов
func CompressWithPool(data []byte, getBuf func() []byte, putBuf func([]byte)) ([]byte, bool, error) {
	if len(data) < CompressionThreshold {
		return data, false, nil
	}

	buf := getBuf()
	defer putBuf(buf)

	var compressed bytes.Buffer
	writer := lz4.NewWriter(&compressed)

	if _, err := writer.Write(data); err != nil {
		return nil, false, fmt.Errorf("failed to compress: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, false, fmt.Errorf("failed to close compressor: %w", err)
	}

	result := compressed.Bytes()

	// Проверяем коэффициент сжатия
	ratio := float64(len(result)) / float64(len(data))
	if ratio >= CompressionRatioThreshold {
		return data, false, nil
	}

	// Копируем результат в буфер из пула если возможно
	if cap(buf) >= len(result) {
		copy(buf, result)
		return buf[:len(result)], true, nil
	}

	// Иначе возвращаем новый буфер
	return result, true, nil
}

// DecompressWithPool распаковывает данные используя пул буферов
func DecompressWithPool(data []byte, compressed bool, getBuf func() []byte, putBuf func([]byte)) ([]byte, error) {
	if !compressed {
		return data, nil
	}

	reader := lz4.NewReader(bytes.NewReader(data))

	buf := getBuf()
	defer putBuf(buf)

	var result bytes.Buffer
	if _, err := result.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}

	decompressed := result.Bytes()
	if len(decompressed) == 0 {
		return nil, errors.New("decompressed data is empty")
	}

	// Копируем в буфер из пула если возможно
	if cap(buf) >= len(decompressed) {
		copy(buf, decompressed)
		return buf[:len(decompressed)], nil
	}

	return decompressed, nil
}
