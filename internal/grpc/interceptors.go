package grpc

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor пишет по записи на каждый gRPC-вызов.
//
// Без него ошибки обработчиков терялись: клиент видел только код статуса,
// а в логах не было ни метода, ни времени выполнения.
func LoggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		st, _ := status.FromError(err)
		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.Duration("duration", time.Since(start)),
			slog.String("code", st.Code().String()),
		}
		if err != nil {
			attrs = append(attrs, slog.String("err", err.Error()))
			log.LogAttrs(ctx, slog.LevelWarn, "gRPC-вызов завершился с ошибкой", attrs...)
		} else {
			log.LogAttrs(ctx, slog.LevelInfo, "gRPC-вызов", attrs...)
		}

		return resp, err
	}
}

// StreamLoggingInterceptor ведёт учёт потоковых вызовов.
func StreamLoggingInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		err := handler(srv, stream)

		st, _ := status.FromError(err)
		attrs := []slog.Attr{
			slog.String("method", info.FullMethod),
			slog.Duration("duration", time.Since(start)),
			slog.String("code", st.Code().String()),
		}
		if err != nil {
			attrs = append(attrs, slog.String("err", err.Error()))
			log.LogAttrs(stream.Context(), slog.LevelWarn, "поток завершился с ошибкой", attrs...)
		} else {
			log.LogAttrs(stream.Context(), slog.LevelInfo, "поток", attrs...)
		}

		return err
	}
}
