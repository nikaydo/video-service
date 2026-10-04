package storage

import (
	"bufio"
	"errors"
	"io"
	"os"
)

// ChunkedFile читает файл фиксированными порциями.
//
// Порции всегда одинакового размера, кроме последней. Так клиент заранее
// знает размер каждого сообщения и не получает короткое сообщение в конце,
// которое пришлось бы обрабатывать отдельно от остальных.
type ChunkedFile struct {
	file   *os.File
	reader *bufio.Reader
	size   int64

	// buf — буфер фиксированной длины, в который читаются данные.
	// Длина не меняется: Read в срез нулевой длины возвращает пустой результат
	// без ошибки, и цикл чтения зациклился бы.
	buf []byte
	// data — срез buf, содержащий прочитанную порцию.
	data []byte
	// pending отмечает, что порция прочитана и ждёт отправки.
	pending bool
}

// newChunkedFile оборачивает открытый файл.
func newChunkedFile(f *os.File, size int64, chunkSize int) *ChunkedFile {
	return &ChunkedFile{
		file:   f,
		reader: bufio.NewReaderSize(f, chunkSize),
		size:   size,
		buf:    make([]byte, chunkSize),
	}
}

// Size возвращает размер файла в байтах.
func (c *ChunkedFile) Size() int64 {
	return c.size
}

// Next читает следующую порцию данных.
//
// Возвращает io.EOF, когда файл прочитан целиком. Данные доступны через
// Chunk до следующего вызова Next: буфер переиспользуется.
func (c *ChunkedFile) Next() error {
	if c.pending {
		return errors.New("предыдущая порция не отправлена: вызовите Next только после отправки")
	}

	n, err := c.reader.Read(c.buf)
	switch {
	case n > 0:
		// Последняя порция может оказаться короче остальных, поэтому
		// достижение конца файла вместе с данными — не ошибка.
		c.data = c.buf[:n]
		c.pending = true
		return nil
	case errors.Is(err, io.EOF):
		return io.EOF
	case err != nil:
		return err
	default:
		// bufio.Reader может вернуть 0 и nil, если внутренний буфер пуст,
		// но файл ещё не закончился. Повторяем попытку, иначе цикл чтения
		// зациклится.
		return c.Next()
	}
}

// Chunk возвращает подготовленную порцию.
//
// До первого успешного Next возвращается nil: отправлять сообщение, пока
// данные не прочитаны, нельзя.
func (c *ChunkedFile) Chunk() []byte {
	if !c.pending {
		return nil
	}
	return c.data
}

// MarkConsumed подтверждает, что порция отправлена.
func (c *ChunkedFile) MarkConsumed() {
	c.data = c.data[:0]
	c.pending = false
}

// Close закрывает файл.
func (c *ChunkedFile) Close() error {
	return c.file.Close()
}
