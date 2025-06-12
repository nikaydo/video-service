package config

import (
	"github.com/joho/godotenv"
)

type Env struct {
	EnvMap map[string]string
}

func ReadEnv() (Env, error) {
	envMap, err := godotenv.Read("./.env")
	if err != nil {
		return Env{}, err
	}
	return Env{EnvMap: envMap}, nil
}
