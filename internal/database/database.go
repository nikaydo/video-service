package database

import (
	"context"
	"fmt"
	"log"
	"main/internal/config"
	"main/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Database struct {
	Env     config.Env
	VideoDB *mongo.Database
}

func (db *Database) Init() {
	clientOptions := options.Client().ApplyURI(db.Env.EnvMap["MONGODB"])
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	db.VideoDB = client.Database("Video")
}

func (db *Database) Add(uuid, token, title string) error {
	collection := db.VideoDB.Collection("vids")
	video := models.Video{Uuid: uuid, Token: token, Title: title}
	_, err := collection.InsertOne(context.Background(), video)
	if err != nil {
		return err
	}
	return nil
}

func (db *Database) Gets(title string) ([]models.SavedVideo, error) {
	collection := db.VideoDB.Collection("vids")
	cursor, err := collection.Find(context.Background(), bson.M{"title": title})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return []models.SavedVideo{}, fmt.Errorf("video with title %q not found", title)
		}
		return []models.SavedVideo{}, err
	}
	var videos []models.SavedVideo
	if err = cursor.All(context.Background(), &videos); err != nil {
		log.Fatal(err)
	}
	return videos, nil
}

func (db *Database) Delete(uuid string) error {
	collection := db.VideoDB.Collection("vids")
	_, err := collection.DeleteOne(context.Background(), bson.M{"uuid": uuid})
	if err != nil {
		return err
	}
	return nil
}
