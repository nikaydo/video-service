// Package grpc реализует gRPC-сервис видео.
package grpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	videopb "github.com/nikaydo/grpc-contract/gen/video"

	"github.com/nikaydo/video-service/internal/database"
	"github.com/nikaydo/video-service/internal/storage"
)

// VideoService — реализация контракта Video.
type VideoService struct {
	videopb.UnimplementedVideoServer

	db       *database.Store
	files    *storage.Store
	log      *slog.Logger
	tagLimit int
}

// New создаёт сервис видео.
func New(db *database.Store, files *storage.Store, log *slog.Logger, tagLimit int) *VideoService {
	if log == nil {
		log = slog.Default()
	}
	if tagLimit < 1 {
		tagLimit = 20
	}
	return &VideoService{db: db, files: files, log: log, tagLimit: tagLimit}
}

// Add сохраняет видео.
//
// Файл сначала пишется на диск, затем сохраняется описание. При сбое второго
// шага файл удаляется: иначе на диске остаются видео, о котором никто не
// знает, и они никогда не будут показаны при очистке.
func (v *VideoService) Add(ctx context.Context, req *videopb.AddRequest) (*videopb.AddResponse, error) {
	userID := int64(req.GetUserId())
	if userID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "некорректный идентификатор пользователя")
	}
	if len(req.GetVideo()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "файл не передан")
	}

	tags := req.GetTags()
	if len(tags.GetTag()) > v.tagLimit {
		return nil, status.Errorf(codes.InvalidArgument,
			"слишком много меток: %d, максимум %d", len(tags.GetTag()), v.tagLimit)
	}

	id, err := v.files.Save(bytes.NewReader(req.GetVideo()), req.GetName())
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrTooLarge):
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		default:
			return nil, internalError(ctx, v.log, "не удалось сохранить видео", err)
		}
	}

	err = v.db.Save(ctx, database.Metadata{
		ID:     id,
		UserID: userID,
		Title:  req.GetName(),
		Tags:   tags.GetTag(),
		Size:   int64(len(req.GetVideo())),
	})
	if err != nil {
		// Описание не сохранилось — файл не нужен.
		if delErr := v.files.Delete(id); delErr != nil {
			v.log.Error("не удалось удалить осиротевший файл",
				"video_id", id, "err", delErr)
		}
		return nil, internalError(ctx, v.log, "не удалось сохранить описание видео", err)
	}

	v.log.Info("видео загружено",
		"video_id", id,
		"user_id", userID,
		"size", len(req.GetVideo()),
	)
	return &videopb.AddResponse{Result: true}, nil
}

// Get возвращает список видео пользователя по названию.
func (v *VideoService) Get(ctx context.Context, req *videopb.GetRequest) (*videopb.GetResponse, error) {
	userID := int64(req.GetUserId())
	if userID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "некорректный идентификатор пользователя")
	}

	list, err := v.db.ListByTitle(ctx, userID, req.GetVideoName())
	if err != nil {
		return nil, internalError(ctx, v.log, "не удалось получить список видео", err)
	}

	videos := make([]*videopb.SavedVideo, 0, len(list))
	for _, m := range list {
		videos = append(videos, &videopb.SavedVideo{
			Uuid:  m.ID,
			Title: m.Title,
		})
	}
	return &videopb.GetResponse{Video: &videopb.Videos{Video: videos}}, nil
}

// Delete удаляет видео пользователя.
//
// Описание и файл удаляются в обоих направлениях: если файл удалить не
// удалось, ошибка возвращается, но описание уже убрано — иначе повторная
// попытка нашла бы запись о несуществующем файле.
func (v *VideoService) Delete(ctx context.Context, req *videopb.DeleteRequest) (*videopb.DeleteResponse, error) {
	userID := int64(req.GetUserId())
	if userID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "некорректный идентификатор пользователя")
	}

	if err := v.db.Delete(ctx, userID, req.GetUuid()); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "видео не найдено")
		}
		return nil, internalError(ctx, v.log, "не удалось удалить описание видео", err)
	}

	if err := v.files.Delete(req.GetUuid()); err != nil && !errors.Is(err, storage.ErrInvalidID) {
		return nil, internalError(ctx, v.log, "описание удалено, но файл остался", err)
	}

	v.log.Info("видео удалено", "video_id", req.GetUuid(), "user_id", userID)
	return &videopb.DeleteResponse{Result: true}, nil
}

// Stream отдаёт видео последовательностью сообщений.
//
// Раньше метод был обычным: он целиком загружал файл в память и отдавал одним
// сообщением, что ограничивало размер видео размером одного сообщения gRPC.
// Теперь файл читается с диска порциями и в памяти держится только одна.
func (v *VideoService) Stream(req *videopb.StreamRequest, srv videopb.Video_StreamServer) error {
	ctx := srv.Context()

	chunked, err := v.files.ChunkReader(req.GetUuid())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return status.Error(codes.NotFound, "видео не найдено")
		}
		return internalError(ctx, v.log, "не удалось открыть видео", err)
	}
	defer func() { _ = chunked.Close() }()

	for {
		if err := chunked.Next(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return internalError(ctx, v.log, "не удалось прочитать видео", err)
		}

		data := chunked.Chunk()
		if err := srv.Send(&videopb.StreamResponse{Video: data}); err != nil {
			// Ошибка отправки — это отмена клиента, а не сбой сервиса.
			v.log.Debug("передача видео прервана",
				"video_id", req.GetUuid(),
				"err", err,
			)
			return err
		}
		chunked.MarkConsumed()
	}
}

// internalError логирует причину и возвращает клиенту нейтральный ответ.
func internalError(ctx context.Context, log *slog.Logger, public string, cause error) error {
	log.LogAttrs(ctx, slog.LevelError, "ошибка сервиса видео",
		slog.String("public", public),
		slog.String("cause", cause.Error()),
	)
	return status.Error(codes.Internal, fmt.Sprintf("%s: %v", public, internalSuffix(cause)))
}

// internalSuffix оставляет в тексте ошибки только тип, без деталей.
func internalSuffix(err error) string {
	var mongoErr interface{ HasErrorLabel(string) bool }
	if errors.As(err, &mongoErr) {
		return "ошибка MongoDB"
	}
	return "внутренняя ошибка"
}
