package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

func withProjectFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/documents/files/get_collection", "/api/v2/documents/files/initialize":
			var request map[string]any
			json.NewDecoder(r.Body).Decode(&request)
			data := []byte("# Context\n")
			path := []byte("index.md")
			h := sha256.New()
			binary.Write(h, binary.BigEndian, uint64(len(path)))
			h.Write(path)
			binary.Write(h, binary.BigEndian, uint64(len(data)))
			h.Write(data)
			json.NewEncoder(w).Encode(map[string]any{"collection": request["collection"], "version": 1, "hash": hex.EncodeToString(h.Sum(nil)), "files": []map[string]any{{"path": "index.md", "size": len(data)}}, "contents": map[string]string{"index.md": base64.StdEncoding.EncodeToString(data)}})
			return
		case "/api/v2/documents/files/read_file":
			json.NewEncoder(w).Encode(map[string]any{"path": "index.md", "content": "# Context\n", "encoding": "utf-8"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
