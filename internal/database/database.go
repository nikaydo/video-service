// Package database отвечает за метаданные видео в MongoDB.
//
// Файлы лежат на диске, MongoDB хранит только описание: идентификатор,
// владельца, название и время загрузки. Это дешевле, чем держать видео в
// документе, и позволяет отдавать файл потоком с диска, не загружая его
// в память целиком.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrNotFound возвращается, когда видео не найдено.
var ErrNotFound = errors.New("видео не найдено")

// Metadata — описание видео.
type Metadata struct {
	// ID — идентификатор файла, одновременно ключ в MongoDB.
	ID string `bson:"_id"`
	// UserID — владелец. Участвует в каждом запросе: без него любой мог бы
	// прочитать или удалить чужое видео, зная его идентификатор.
	UserID int64 `bson:"user_id"`
	// Title — название, по которому видео находят.
	Title string `bson:"title"`
	// Tags — метки, заданные при загрузке.
	Tags []string `bson:"tags,omitempty"`
	// Size — размер файла в байтах.
	Size int64 `bson:"size"`
	// CreatedAt — время загрузки.
	CreatedAt time.Time `bson:"created_at"`
}

// Store — обёртка над клиентом MongoDB.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
	videos *mongo.Collection
}

// New подключается к MongoDB и создаёт необходимые индексы.
func New(ctx context.Context, uri, dbName string) (*Store, error) {
	opts := options.Client().ApplyURI(uri)
	// Сервер недоступен не должен приводить к падению при старте: клиент
	// подключается лениво и повторяет попытки сам.
	opts.SetServerSelectionTimeout(10 * time.Second)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("не удалось подключиться к MongoDB: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("MongoDB недоступна: %w", err)
	}

	col := client.Database(dbName).Collection("videos")
	if err := ensureIndexes(ctx, col); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}

	return &Store{client: client, db: client.Database(dbName), videos: col}, nil
}

// ensureIndexes создаёт индексы, нужные для запросов сервиса.
func ensureIndexes(ctx context.Context, col *mongo.Collection) error {
	// Поиск по названию выполняется на каждый запрос списка.
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "title", Value: 1}},
		Options: options.Index().SetName("videos_title"),
	})
	if err != nil {
		return fmt.Errorf("не удалось создать индекс по названию: %w", err)
	}

	// Список видео пользователя: выборка по владельцу с сортировкой по дате.
	_, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "user_id", Value: 1},
			{Key: "created_at", Value: -1},
		},
		Options: options.Index().SetName("videos_user_created"),
	})
	if err != nil {
		return fmt.Errorf("не удалось создать индекс по владельцу: %w", err)
	}
	return nil
}

// Close отключает клиент.
func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// Save сохраняет описание видео.
func (s *Store) Save(ctx context.Context, m Metadata) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if _, err := s.videos.InsertOne(ctx, m); err != nil {
		return fmt.Errorf("не удалось сохранить описание видео: %w", err)
	}
	return nil
}

// GetByID возвращает описание видео.
//
// Владелец передаётся обязательным параметром и участвует в условии: без
// этой проверки любой, знающий идентификатор, получил бы чужое видео.
func (s *Store) GetByID(ctx context.Context, userID int64, id string) (Metadata, error) {
	var m Metadata
	err := s.videos.FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Metadata{}, ErrNotFound
		}
		return Metadata{}, fmt.Errorf("не удалось получить описание видео: %w", err)
	}
	return m, nil
}

// ListByTitle возвращает видео пользователя по названию.
//
// Раньше поиск шёл только по названию, без учёта владельца: один пользователь
// видел видео другого, если названия совпадали.
func (s *Store) ListByTitle(ctx context.Context, userID int64, title string) ([]Metadata, error) {
	filter := bson.M{"user_id": userID}
	if title != "" {
		filter["title"] = title
	}

	cur, err := s.videos.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список видео: %w", err)
	}
	defer func() {
		// Ошибка закрытия курсора здесь не критична: чтение уже завершено,
		// а log.Fatal в этом месте ронял бы процесс из-за второстепенной
		// проблемы.
		_ = cur.Close(ctx)
	}()

	out := make([]Metadata, 0)
	for cur.Next(ctx) {
		var m Metadata
		// Ошибку декодирования нужно возвращать, а не игнорировать: иначе в
		// список попал бы частично заполненный элемент.
		if err := cur.Decode(&m); err != nil {
			return nil, fmt.Errorf("ошибка чтения описания видео: %w", err)
		}
		out = append(out, m)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("ошибка перебора видео: %w", err)
	}
	return out, nil
}

// Delete удаляет описание видео пользователя.
//
// Владелец участвует в условии удаления. Возвращает ErrNotFound, если записи
// нет — в том числе когда она принадлежит другому пользователю.
func (s *Store) Delete(ctx context.Context, userID int64, id string) error {
	res, err := s.videos.DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return fmt.Errorf("не удалось удалить описание видео: %w", err)
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}
