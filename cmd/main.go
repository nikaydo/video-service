// Command service — gRPC-сервис хранения и выдачи видео.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	videopb "github.com/nikaydo/grpc-contract/gen/video"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	grpcsrv "github.com/nikaydo/video-service/internal/grpc"

	"github.com/nikaydo/video-service/internal/config"
	"github.com/nikaydo/video-service/internal/database"
	"github.com/nikaydo/video-service/internal/storage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("сервис завершился с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("конфигурация загружена", "addr", cfg.Addr(), "video_dir", cfg.VideoDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	db, err := database.New(startupCtx, cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := db.Close(closeCtx); err != nil {
			log.Error("не удалось отключиться от MongoDB", "err", err)
		}
	}()

	files, err := storage.New(cfg.VideoDir, cfg.MaxVideoBytes, int64(cfg.StreamChunkSize()))
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return err
	}

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(int(cfg.MaxMessageBytes)),
		grpc.MaxSendMsgSize(int(cfg.MaxMessageBytes)),
		// Без интерцептора паника в обработчике обрушивает процесс:
		// обработчики gRPC выполняются в горутинах сервера.
		grpc.UnaryInterceptor(grpcsrv.LoggingInterceptor(log)),
		grpc.ChainStreamInterceptor(grpcsrv.StreamLoggingInterceptor(log)),
	)
	videopb.RegisterVideoServer(server, grpcsrv.New(db, files, log, cfg.TagLimit))
	reflection.Register(server)

	errCh := make(chan error, 1)
	go func() {
		log.Info("gRPC-сервер запущен", "addr", cfg.Addr())
		if err := server.Serve(lis); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал завершения")
	}

	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		log.Info("сервер остановлен")
		return nil
	case <-time.After(cfg.ShutdownTimeout):
		server.Stop()
		log.Warn("сервер остановлен принудительно")
		return <-errCh
	}
}
