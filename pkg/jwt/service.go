package jwt

import (
	"api-gateway/internal/config"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type JWTService struct {
	config config.JWTConfig
}

func NewJWTService(cfg config.JWTConfig) *JWTService {
	return &JWTService{config: cfg}
}

func (j *JWTService) CreateToken(username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": username,
		"exp":      j.config.Expiration,
	})

	tokenString, err := token.SignedString(j.config.Secret)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

func (j *JWTService) VerifyToken(tokenString string) error {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return j.config.Secret, nil
	})
	if err != nil {
		return err
	}

	if !token.Valid {
		return fmt.Errorf("invalid token")
	}

	return nil
}
