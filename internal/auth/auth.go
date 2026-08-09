package auth

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func GenerateToken(userID string) (string, error) {
	jwt_secret := os.Getenv("JWT_SECRET")
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString([]byte(jwt_secret))
}

func ValidateToken(tokenstr string) (jwt.Claims, error) {
	jwt_secret := os.Getenv("JWT_SECRET")

	token, err := jwt.Parse(tokenstr, func(t *jwt.Token) (any, error) {
		_, ok := t.Method.(*jwt.SigningMethodHMAC)
		if !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(jwt_secret), nil
	})

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	return token.Claims.(jwt.MapClaims), nil
}
