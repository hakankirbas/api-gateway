package handlers

import (
	"api-gateway/pkg/jwt"
	"encoding/json"
	"fmt"
	"net/http"
)

type LoginHandler struct {
	jwtService *jwt.JWTService
}

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func NewLoginHandler(jwtService *jwt.JWTService) *LoginHandler {
	return &LoginHandler{jwtService: jwtService}
}

func (lh *LoginHandler) Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var u User
	json.NewDecoder(r.Body).Decode(&u)

	if u.Username == "Hako" && u.Password == "123" {
		tokenString, err := lh.jwtService.CreateToken(u.Username)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "Failed to create token")
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, tokenString)
		return
	} else {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "Invalid credentials")
	}
}
