package grpc

import (
	"context"
	"fmt"
	"main/internal/database"
	"os"

	"github.com/google/uuid"
	video "github.com/nikaydo/grpc-contract/gen/video"
)

type VideoService struct {
	video.UnimplementedVideoServer
	Db database.Database
}

func (v *VideoService) Stream(ctx context.Context, req *video.StreamRequest) (*video.StreamResponse, error) {
	videoData, err := os.ReadFile(v.Db.Env.EnvMap["PATH_VIDEO"] + req.Uuid + ".mp4")
	if err != nil {
		fmt.Printf("Ошибка при чтении видео:%s Uuid: %s", err.Error(), req.Uuid)
		return &video.StreamResponse{}, err
	}
	return &video.StreamResponse{Video: videoData}, nil
}

func (v *VideoService) Get(ctx context.Context, req *video.GetRequest) (*video.GetResponse, error) {
	result, err := v.Db.Gets(req.VideoName)
	if err != nil {
		return &video.GetResponse{}, err
	}
	var videos []*video.SavedVideo
	for _, i := range result {
		videos = append(videos, &video.SavedVideo{
			Uuid:  i.Uuid,
			Title: i.Title,
		})
	}
	s := video.Videos{Video: videos}
	return &video.GetResponse{Video: &s}, nil
}

func (v *VideoService) Add(ctx context.Context, req *video.AddRequest) (*video.AddResponse, error) {
	id := uuid.New()
	if err := os.WriteFile(v.Db.Env.EnvMap["PATH_VIDEO"]+id.String()+".mp4", req.Video, 0644); err != nil {
		return &video.AddResponse{}, err
	}
	if err := v.Db.Add(id.String(), req.Token, req.Name); err != nil {
		return &video.AddResponse{}, err
	}
	return &video.AddResponse{}, nil
}

func (v *VideoService) Delete(ctx context.Context, req *video.DeleteRequest) (*video.DeleteResponse, error) {
	if err := v.Db.Delete(req.Uuid); err != nil {
		return &video.DeleteResponse{}, err
	}
	return &video.DeleteResponse{Result: true}, nil
}
