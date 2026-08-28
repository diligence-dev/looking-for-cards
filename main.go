package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/diligence-dev/looking-for-cards/server"
)

//go:embed frontend
var frontendFS embed.FS

func main() {
	dataDir := getEnv("DATA_DIR", "data")
	port := getEnv("PORT", "8080")
	dbPath := dataDir + "/data.db"

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	db, err := server.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	frontend, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		log.Fatalf("Failed to create frontend sub FS: %v", err)
	}

	srv := server.NewServer(db, frontend)

	if err := srv.ListenAndServe(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
