package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAcceptsValidConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MONGODB_URI", "mongodb://mongo:27017")
	t.Setenv("VIDEO_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}

	if cfg.Port != 50053 {
		t.Errorf("Port = %d, ожидалось 50053", cfg.Port)
	}
	if cfg.MongoDB != "video" {
		t.Errorf("MongoDB = %q, ожидалось video", cfg.MongoDB)
	}
	if cfg.StreamChunkSize() != 65536 {
		t.Errorf("StreamChunkSize = %d, ожидалось 65536", cfg.StreamChunkSize())
	}
	if cfg.VideoDir != dir {
		t.Errorf("VideoDir = %q, ожидалось %q", cfg.VideoDir, dir)
	}
}

func TestLoadCreatesVideoDir(t *testing.T) {
	// Каталог хранения должен существовать до старта: иначе первая же
	// загрузка завершится ошибкой.
	dir := filepath.Join(t.TempDir(), "nested", "video")
	t.Setenv("MONGODB_URI", "mongodb://mongo:27017")
	t.Setenv("VIDEO_DIR", dir)

	if _, err := Load(); err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if info, err := os.Stat(dir); err != nil {
		t.Errorf("каталог не создан: %v", err)
	} else if !info.IsDir() {
		t.Error("по пути создан не каталог")
	}
}

func TestValidateRejectsBadMongoURI(t *testing.T) {
	for _, uri := range []string{
		"postgres://localhost/db",
		"http://mongo:27017",
		"CHANGE_ME",
		"mongo:27017",
	} {
		t.Run(uri, func(t *testing.T) {
			t.Setenv("MONGODB_URI", uri)
			t.Setenv("VIDEO_DIR", t.TempDir())
			if _, err := Load(); err == nil {
				t.Fatalf("URI %q не должен приниматься", uri)
			}
		})
	}
}

func TestValidateAcceptsMongoSrvURI(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb+srv://user:pass@cluster.example.net/db")
	t.Setenv("VIDEO_DIR", t.TempDir())

	if _, err := Load(); err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
}

func TestValidateRequiresMongoURI(t *testing.T) {
	t.Setenv("VIDEO_DIR", t.TempDir())

	_, err := Load()
	if err == nil {
		t.Fatal("отсутствие MONGODB_URI должно приводить к ошибке")
	}
	if !strings.Contains(err.Error(), "MONGODB_URI") {
		t.Errorf("ошибка не упоминает поле: %v", err)
	}
}

func TestValidateRejectsEmptyVideoDir(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://mongo:27017")
	t.Setenv("VIDEO_DIR", "   ")

	if _, err := Load(); err == nil {
		t.Fatal("пустой каталог хранения не должен приниматься")
	}
}

func TestValidateRejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"0", "70000", "-1"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("MONGODB_URI", "mongodb://mongo:27017")
			t.Setenv("VIDEO_DIR", t.TempDir())
			t.Setenv("PORT", port)

			if _, err := Load(); err == nil {
				t.Fatalf("порт %s не должен приниматься", port)
			}
		})
	}
}

func TestValidateRejectsTinyChunkSize(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://mongo:27017")
	t.Setenv("VIDEO_DIR", t.TempDir())
	t.Setenv("STREAM_CHUNK_BYTES", "16")

	if _, err := Load(); err == nil {
		t.Fatal("слишком маленькая порция не должна приниматься")
	}
}

func TestStreamChunkSizeNeverExceedsMessageLimit(t *testing.T) {
	// Если запрошенная порция больше предела сообщения, её нужно урезать:
	// иначе последнее сообщение превысит лимит и передача оборвётся.
	cfg := Config{
		MaxMessageBytes:  1024,
		StreamChunkBytes: 1 << 20,
	}
	if got := cfg.StreamChunkSize(); got > int(cfg.MaxMessageBytes) {
		t.Errorf("размер порции %d превышает предел сообщения %d", got, cfg.MaxMessageBytes)
	}

	// Некорректно малый предел не должен приводить к нулевому размеру порции:
	// это вызвало бы бесконечный цикл отправки.
	cfg = Config{MaxMessageBytes: 16, StreamChunkBytes: 4096}
	if got := cfg.StreamChunkSize(); got < 1024 {
		t.Errorf("размер порции %d слишком мал", got)
	}
}
