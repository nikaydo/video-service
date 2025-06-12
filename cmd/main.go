package main

import (
	"fmt"
	"log"
	"main/internal/config"
	"main/internal/database"
	g "main/internal/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"

	video "github.com/nikaydo/grpc-contract/gen/video"
	"google.golang.org/grpc"
)

func main() {
	env, err := config.ReadEnv()
	if err != nil {
		log.Fatal("Error loading .env file:", err)
	}
	log.Println("Database auth succesful read")
	Db := database.Database{Env: env}
	Db.Init()
	log.Println("Database succesful connected")
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%s", env.EnvMap["HOST"], env.EnvMap["PORT"]))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(100<<20), // 100 MB на приём
		grpc.MaxSendMsgSize(100<<20), // 100 MB на отправку
	)
	video.RegisterVideoServer(grpcServer, &g.VideoService{Db: Db})
	log.Println("gRPC server started on ", fmt.Sprintf("%s:%s", env.EnvMap["HOST"], env.EnvMap["PORT"]))
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	<-quit
	log.Println("Shutting down server...")
	grpcServer.GracefulStop()
	log.Println("Server gracefully stopped")
}
