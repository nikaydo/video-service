// Package storage отвечает за хранение видеофайлов на диске.
//
// Имя файла строится только из идентификатора, который сервис сам сгенерировал
// в виде UUID. Значение из запроса в путь не подставляется никогда: иначе
// последовательность вида ../../etc/passwd позволила бы прочитать любой файл
// на сервере. Дополнительно имя проверяется на соответствие UUID.
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ErrNotFound возвращается, когда файла нет.
var ErrNotFound = errors.New("файл не найден")

// ErrInvalidID возвращается, когда идентификатор не является UUID.
var ErrInvalidID = errors.New("некорректный идентификатор видео")

// ErrTooLarge возвращается, когда файл превышает допустимый размер.
var ErrTooLarge = errors.New("файл превышает допустимый размер")

// Store хранит видеофайлы в каталоге.
type Store struct {
	dir       string
	maxBytes  int64
	chunkSize int
}

// New создаёт хранилище в каталоге dir.
func New(dir string, maxBytes, chunkSize int64) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("каталог хранения не может быть пустым")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог %q: %w", dir, err)
	}
	if chunkSize < 1024 {
		chunkSize = 65536
	}
	return &Store{dir: dir, maxBytes: maxBytes, chunkSize: int(chunkSize)}, nil
}

// Dir возвращает каталог хранения.
func (s *Store) Dir() string {
	return s.dir
}

// pathFor возвращает путь к файлу по идентификатору.
func (s *Store) pathFor(id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return filepath.Join(s.dir, id+".mp4"), nil
}

// Save записывает видео под новым идентификатором и возвращает его.
//
// Запись идёт во временный файл с последующим переименованием: переименование
// в пределах одного каталога атомарно, поэтому читатель никогда не увидит
// наполовину загруженный файл.
func (s *Store) Save(r io.Reader, name string) (string, error) {
	if r == nil {
		return "", errors.New("нет данных для сохранения")
	}

	id := uuid.NewString()

	target, err := s.pathFor(id)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("не удалось создать временный файл: %w", err)
	}
	tmpName := tmp.Name()

	// Файл 0644 делал видео доступным на чтение всем пользователям системы;
	// 0600 ограничивает доступ процессом сервиса.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("не удалось задать права на файл: %w", err)
	}

	// Ограничение размера: без него один запрос мог заполнить диск.
	reader := io.Reader(r)
	if s.maxBytes > 0 {
		reader = io.LimitReader(r, s.maxBytes+1)
	}

	written, err := io.Copy(tmp, reader)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("не удалось записать видео: %w", err)
	}
	if s.maxBytes > 0 && written > s.maxBytes {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("%w: больше %d байт", ErrTooLarge, s.maxBytes)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("не удалось закрыть файл: %w", err)
	}

	if written == 0 {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", errors.New("файл пуст")
	}

	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("не удалось сохранить видео: %w", err)
	}
	return id, nil
}

// Delete удаляет видеофайл.
//
// Отсутствующий файл не считается ошибкой: запись в базе могла остаться после
// сбоя удаления, и повторная попытка должна завершаться успехом.
func (s *Store) Delete(id string) error {
	path, err := s.pathFor(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("не удалось удалить видео: %w", err)
	}
	return nil
}

// ChunkReader возвращает последовательное чтение файла порциями.
//
// Файл открывается для чтения и передаётся вызывающему, который обязан его
// закрыть. Возвращается буфер фиксированного размера: при потоковой передаче
// по gRPC размер порции должен быть известен заранее.
func (s *Store) ChunkReader(id string) (*ChunkedFile, error) {
	path, err := s.pathFor(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("не удалось открыть видео: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("не удалось получить сведения о файле: %w", err)
	}
	return newChunkedFile(f, info.Size(), s.chunkSize), nil
}

// Exists сообщает, есть ли файл видео.
func (s *Store) Exists(id string) (bool, error) {
	path, err := s.pathFor(id)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("не удалось проверить наличие видео: %w", err)
	}
}
