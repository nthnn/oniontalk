/*
 * Copyright (c) 2025 - Nathanne Isip
 * This file is part of OnionTalk.
 *
 * OnionTalk is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published
 * by the Free Software Foundation, either version 3 of the License,
 * or (at your option) any later version.
 *
 * OnionTalk is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with OnionTalk. If not, see <https://www.gnu.org/licenses/>.
 */
package main

import (
	"database/sql"
	"encoding/json"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
)

type Client struct {
	conn     *websocket.Conn
	username string
	room     string
}

type EncryptedContent struct {
	Encrypted []int `json:"encrypted"`
	IV        []int `json:"iv"`
}

type Message struct {
	Type     string           `json:"type"`
	Username string           `json:"username"`
	Content  EncryptedContent `json:"content"`
	Room     string           `json:"room"`
}

type Room struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan Message)
	upgrader   = websocket.Upgrader{}
	db         *sql.DB
	clientsMux sync.Mutex
	roomUsers  = make(map[string]int)
	roomMux    sync.Mutex
)

func sanitizeInput(input string) string {
	sanitized := html.EscapeString(input)
	sanitized = strings.ReplaceAll(sanitized, "&lt;script&gt;", "")
	sanitized = strings.ReplaceAll(sanitized, "&lt;/script&gt;", "")

	return sanitized
}

func validateRoomName(name string) bool {
	if len(name) == 0 || len(name) > 50 {
		return false
	}

	for _, char := range name {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.", char) {
			return false
		}
	}

	return true
}

func updateRoomUserCount(roomName string, delta int) {
	roomMux.Lock()
	defer roomMux.Unlock()

	roomUsers[roomName] += delta
	if roomUsers[roomName] <= 0 {
		delete(roomUsers, roomName)

		stmt, err := db.Prepare("DELETE FROM rooms WHERE name = ?")
		if err != nil {
			log.Printf("Error preparing delete statement: %v", err)
			return
		}
		defer stmt.Close()

		_, err = stmt.Exec(roomName)
		if err != nil {
			log.Printf("Error deleting room %s: %v", roomName, err)
			return
		}
		log.Printf("Room \"%s\" deleted due to inactivity", roomName)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Websocket upgrade error: %v", err)
		return
	}
	defer ws.Close()

	client := &Client{conn: ws}
	clientsMux.Lock()
	clients[client] = true
	clientsMux.Unlock()

	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("Error: %v", err)
			clientsMux.Lock()
			delete(clients, client)
			clientsMux.Unlock()

			if client.room != "" {
				updateRoomUserCount(client.room, -1)
			}
			break
		}

		msg.Username = sanitizeInput(msg.Username)
		msg.Room = sanitizeInput(msg.Room)

		if !validateRoomName(msg.Room) {
			log.Printf("Invalid room name attempt: %s", msg.Room)
			continue
		}

		switch msg.Type {
		case "join":
			if client.room != "" {
				updateRoomUserCount(client.room, -1)
			}
			client.username = msg.Username
			client.room = msg.Room

			updateRoomUserCount(msg.Room, 1)

		case "typing", "message":
			broadcast <- msg
		}
	}
}

func handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var room Room
	if err := json.NewDecoder(r.Body).Decode(&room); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	room.Name = sanitizeInput(room.Name)
	if !validateRoomName(room.Name) {
		http.Error(w, "Invalid room name", http.StatusBadRequest)
		return
	}

	stmt, err := db.Prepare("SELECT password FROM rooms WHERE name = ?")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	var storedPassword string
	err = stmt.QueryRow(room.Name).Scan(&storedPassword)
	if err == sql.ErrNoRows {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if storedPassword != room.Password {
		http.Error(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var room Room
	if err := json.NewDecoder(r.Body).Decode(&room); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	room.Name = sanitizeInput(room.Name)
	if !validateRoomName(room.Name) {
		http.Error(w, "Invalid room name", http.StatusBadRequest)
		return
	}

	stmt, err := db.Prepare("SELECT EXISTS(SELECT 1 FROM rooms WHERE name = ?)")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	var exists bool
	err = stmt.QueryRow(room.Name).Scan(&exists)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if exists {
		stmt, err := db.Prepare("SELECT password FROM rooms WHERE name = ?")
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer stmt.Close()

		var storedPassword string
		err = stmt.QueryRow(room.Name).Scan(&storedPassword)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if storedPassword != room.Password {
			http.Error(w, "Invalid password", http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
		return
	}

	stmt, err = db.Prepare("INSERT INTO rooms (name, password) VALUES (?, ?)")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(room.Name, room.Password)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	roomMux.Lock()
	roomUsers[room.Name] = 0
	roomMux.Unlock()

	w.WriteHeader(http.StatusCreated)
}

func handleMessages() {
	for {
		msg := <-broadcast
		clientsMux.Lock()

		for client := range clients {
			if client.room == msg.Room {
				err := client.conn.WriteJSON(msg)

				if err != nil {
					log.Printf("Error: %v", err)
					client.conn.Close()
					delete(clients, client)
				}
			}
		}

		clientsMux.Unlock()
	}
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "./chat.db")

	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS rooms (
            name TEXT PRIMARY KEY,
            password TEXT NOT NULL
        )
    `)

	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/create-room", handleCreateRoom)
	http.HandleFunc("/join-room", handleJoinRoom)
	http.Handle("/", http.FileServer(http.Dir("static")))

	go handleMessages()

	log.Println("Server starting at localhost:8080...")
	err = http.ListenAndServe("localhost:8080", nil)

	if err != nil {
		log.Fatal("Web socket: ", err)
	}
}
