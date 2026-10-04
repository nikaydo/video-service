package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore создаёт хранилище во временном каталоге.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), 1024*1024, 4096)
	if err != nil {
		t.Fatalf("New вернул ошибку: %v", err)
	}
	return s
}

func TestSaveAndDelete(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Save(bytes.NewReader([]byte("видео")), "ролик")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	exists, err := s.Exists(id)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("сохранённый файл не найден")
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, _ = s.Exists(id)
	if exists {
		t.Error("файл остался после удаления")
	}
}

// TestDeleteMissingFileIsNotError — повторное удаление должно проходить.
//
// Запись в базе могла остаться после сбоя удаления, поэтому повторная
// попытка не должна падать.
func TestDeleteMissingFileIsNotError(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Save(bytes.NewReader([]byte("видео")), "x")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("первое удаление: %v", err)
	}
	if err := s.Delete(id); err != nil {
		t.Errorf("повторное удаление должно быть безошибочным, получено %v", err)
	}
}

// TestPathTraversalIsRejected — главный тест на устранение дыры.
//
// Раньше путь к файлу строился конкатенацией: PATH_VIDEO + uuid + ".mp4".
// Значение вида ../../etc/passwd позволяло прочитать любой файл на сервере.
func TestPathTraversalIsRejected(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 1024*1024, 4096)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Снаружи каталога кладём файл, который не должен быть доступен.
	secret := filepath.Join(dir, "..", "secret.txt")
	if err := os.WriteFile(secret, []byte("секретные данные"), 0o600); err != nil {
		t.Fatalf("не удалось создать файл-жертву: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	attacks := []string{
		"../secret.txt",
		"../../etc/passwd",
		"..%2Fsecret.txt",
		"/etc/passwd",
		"./../../secret",
		"a/../../secret.txt",
		strings.Repeat("../", 20) + "etc/passwd",
	}

	for _, attack := range attacks {
		t.Run(attack, func(t *testing.T) {
			// Exists
			if _, err := s.Exists(attack); !errors.Is(err, ErrInvalidID) {
				t.Errorf("Exists(%q) = %v, ожидался ErrInvalidID", attack, err)
			}
			// Delete
			if err := s.Delete(attack); !errors.Is(err, ErrInvalidID) {
				t.Errorf("Delete(%q) = %v, ожидался ErrInvalidID", attack, err)
			}
			// ChunkReader
			if _, err := s.ChunkReader(attack); !errors.Is(err, ErrInvalidID) {
				t.Errorf("ChunkReader(%q) = %v, ожидался ErrInvalidID", attack, err)
			}
		})
	}

	// Ни один из путей не должен выйти за пределы каталога хранилища.
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("файл вне каталога хранилища оказался затронут: %v", err)
	}
}

// TestGeneratedIDIsUsedForPath проверяет, что имя файла строится только из
// сгенерированного идентификатора.
func TestGeneratedIDIsUsedForPath(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 1024*1024, 4096)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Название из запроса содержит символы пути — оно попадает только в
	// метаданные, но не в имя файла.
	evilName := "../../../etc/passwd"
	id, err := s.Save(bytes.NewReader([]byte("данные")), evilName)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, id+".mp4")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файл не создан по ожидаемому пути %q: %v", path, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") || strings.Contains(e.Name(), "passwd") {
			t.Errorf("в каталоге появился файл с чужим именем: %s", e.Name())
		}
	}
}

func TestSaveRejectsEmptyData(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Save(bytes.NewReader(nil), "x"); err == nil {
		t.Fatal("пустые данные должны отклоняться")
	}
}

func TestSaveRejectsOversizedFile(t *testing.T) {
	s, err := New(t.TempDir(), 1024, 4096)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Данные больше предела: файл не должен остаться на диске.
	big := bytes.Repeat([]byte("A"), 2048)
	if _, err := s.Save(bytes.NewReader(big), "большое"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ожидалась ErrTooLarge, получено %v", err)
	}

	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".upload-") {
			continue
		}
		t.Errorf("временный файл не был удалён: %s", e.Name())
	}
}

func TestSaveRejectsNilReader(t *testing.T) {
	s := newTestStore(t)

	//nolint:staticcheck // намеренно проверяем поведение при nil
	if _, err := s.Save(nil, "x"); err == nil {
		t.Fatal("nil-источник должен отклоняться")
	}
}

func TestNewRejectsEmptyDir(t *testing.T) {
	if _, err := New("   ", 1024, 4096); err == nil {
		t.Fatal("пустой каталог должен отклоняться")
	}
}

func TestChunkReaderMissingFile(t *testing.T) {
	s := newTestStore(t)

	// UUID корректный, но файла нет.
	if _, err := s.ChunkReader("6ba7b810-9dad-11d1-80b4-00c04fd430c8"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
}

func TestChunkReaderReturnsWholeFile(t *testing.T) {
	s := newTestStore(t)

	// Данные меньше одной порции.
	small := []byte("маленькое видео")
	id, err := s.Save(bytes.NewReader(small), "x")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	chunked, err := s.ChunkReader(id)
	if err != nil {
		t.Fatalf("ChunkReader: %v", err)
	}
	defer func() { _ = chunked.Close() }()

	var got []byte
	for {
		err := chunked.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, chunked.Chunk()...)
		chunked.MarkConsumed()
	}

	if !bytes.Equal(got, small) {
		t.Errorf("прочитано %q, ожидалось %q", got, small)
	}
	if chunked.Size() != int64(len(small)) {
		t.Errorf("Size = %d, ожидалось %d", chunked.Size(), len(small))
	}
}

func TestChunkReaderSplitsIntoChunks(t *testing.T) {
	chunkSize := 4096
	s, err := New(t.TempDir(), 1024*1024, int64(chunkSize))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Данные заметно больше порции: должно быть несколько порций.
	big := bytes.Repeat([]byte("B"), chunkSize*3+17)
	id, err := s.Save(bytes.NewReader(big), "большое")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	chunked, err := s.ChunkReader(id)
	if err != nil {
		t.Fatalf("ChunkReader: %v", err)
	}
	defer func() { _ = chunked.Close() }()

	chunks := 0
	var got []byte
	for {
		err := chunked.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		part := chunked.Chunk()
		if len(part) > chunkSize {
			t.Errorf("порция %d байт больше допустимых %d", len(part), chunkSize)
		}
		chunks++
		got = append(got, part...)
		chunked.MarkConsumed()
	}

	if chunks < 4 {
		t.Errorf("порций %d, ожидалось не меньше 4", chunks)
	}
	if !bytes.Equal(got, big) {
		t.Error("собранные порции не совпадают с исходными данными")
	}
}

func TestChunkReaderRejectsDoubleNext(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Save(bytes.NewReader([]byte("данные")), "x")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	chunked, err := s.ChunkReader(id)
	if err != nil {
		t.Fatalf("ChunkReader: %v", err)
	}
	defer func() { _ = chunked.Close() }()

	if err := chunked.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Второй Next без MarkConsumed перезаписал бы буфер, и клиент получил бы
	// повреждённые данные.
	if err := chunked.Next(); err == nil {
		t.Error("повторный Next без подтверждения должен приводить к ошибке")
	}
}

func TestFilePermissionsAreRestrictive(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 1024*1024, 4096)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id, err := s.Save(bytes.NewReader([]byte("данные")), "x")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, id+".mp4"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// 0644 делал видео доступным на чтение всем пользователям системы.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("права %o слишком широкие: видео доступно другим пользователям", perm)
	}
}
