// Package config загружает и проверяет конфигурацию сервиса.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// placeholder — значение-заглушка из .env.example.
const placeholder = "CHANGE_ME"

// Config содержит конфигурацию сервиса.
type Config struct {
	MongoURI string `env:"MONGODB_URI,required"`
	MongoDB  string `env:"MONGODB_NAME" envDefault:"video"`

	// VideoDir — каталог хранения видеофайлов. Путь проверяется при старте,
	// потому что от него зависит безопасность: имена файлов строятся из
	// идентификаторов, и каталог должен существовать.
	VideoDir string `env:"VIDEO_DIR" envDefault:"./storage/video"`

	// gRPC.
	Host string `env:"HOST" envDefault:"0.0.0.0"`
	Port int    `env:"PORT" envDefault:"50053"`

	// Пределы. Размер сообщения ограничен, потому что видео передаётся
	// целиком; файл целиком в память не помещается.
	MaxMessageBytes  int64         `env:"MAX_MESSAGE_BYTES" envDefault:"26214400"` // 25 МиБ
	MaxVideoBytes    int64         `env:"MAX_VIDEO_BYTES"   envDefault:"20971520"` // 20 МиБ
	StreamChunkBytes int           `env:"STREAM_CHUNK_BYTES" envDefault:"65536"`   // 64 КиБ
	ShutdownTimeout  time.Duration `env:"SHUTDOWN_TIMEOUT"   envDefault:"30s"`
	// TagLimit ограничивает число меток при загрузке.
	TagLimit int `env:"TAG_LIMIT" envDefault:"20"`
}

// Addr возвращает адрес gRPC-сервера.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// StreamChunkSize возвращает размер порции при потоковой передаче.
//
// Значение округляется вниз до числа байт, помещающихся в одно сообщение:
// иначе последняя порция превысила бы лимит и передача оборвалась бы.
func (c Config) StreamChunkSize() int {
	const headerOverhead = 4096
	limit := c.MaxMessageBytes - headerOverhead
	if limit < 1024 {
		limit = 1024
	}
	chunk := int64(c.StreamChunkBytes)
	if chunk < limit {
		return int(chunk)
	}
	return int(limit)
}

// Load читает конфигурацию и проверяет её.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(".env"); statErr == nil {
			return Config{}, fmt.Errorf("не удалось прочитать .env: %w", err)
		}
	}

	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("не удалось разобрать конфигурацию: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.EnsureVideoDir(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate проверяет значения, которые env-парсер не может проверить сам.
func (c Config) Validate() error {
	var problems []string

	switch {
	case c.MongoURI == "":
		problems = append(problems, "MONGODB_URI не задан")
	case c.MongoURI == placeholder:
		problems = append(problems, "MONGODB_URI остался со значением-заглушкой "+placeholder)
	default:
		if !strings.HasPrefix(c.MongoURI, "mongodb://") && !strings.HasPrefix(c.MongoURI, "mongodb+srv://") {
			problems = append(problems, "MONGODB_URI должен начинаться с mongodb:// или mongodb+srv://")
		}
	}

	if c.Port < 1 || c.Port > 65535 {
		problems = append(problems, fmt.Sprintf("PORT=%d вне диапазона 1-65535", c.Port))
	}
	if c.MaxVideoBytes <= 0 {
		problems = append(problems, "MAX_VIDEO_BYTES должен быть больше нуля")
	}
	if c.StreamChunkBytes < 1024 {
		problems = append(problems, "STREAM_CHUNK_BYTES должен быть не меньше 1024")
	}
	if c.MaxMessageBytes <= 0 {
		problems = append(problems, "MAX_MESSAGE_BYTES должен быть больше нуля")
	}

	if len(problems) > 0 {
		return fmt.Errorf("некорректная конфигурация:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// EnsureVideoDir создаёт каталог хранения видео, если его нет.
func (c Config) EnsureVideoDir() error {
	if strings.TrimSpace(c.VideoDir) == "" {
		return errors.New("VIDEO_DIR не может быть пустым")
	}
	if err := os.MkdirAll(c.VideoDir, 0o750); err != nil {
		return fmt.Errorf("не удалось создать каталог для видео %q: %w", c.VideoDir, err)
	}
	return nil
}
