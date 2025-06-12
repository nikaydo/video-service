package models

type Video struct {
	Uuid  string `bson:"uuid"`
	Token string `bson:"token"`
	Title string `bson:"title"`
}

type SavedVideo struct {
	Video []byte
	Title string
	Uuid  string
}
type V struct {
	S []*SavedVideo
}
