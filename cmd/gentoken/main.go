package main

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	secret := "3ce499d7139f7a7ff6cfeebbf5ab9fdab3acb7659735db71f9caf24f8cec3373"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"merchant_id": "00000000-0000-0000-0000-000000000001",
		"exp":         time.Now().Add(72 * time.Hour).Unix(),
	})
	signed, _ := token.SignedString([]byte(secret))
	fmt.Println(signed)
}
