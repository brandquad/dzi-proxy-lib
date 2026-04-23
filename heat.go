package dziproxylib

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func heatHandler(w http.ResponseWriter, r *http.Request) {

	urlPath := strings.TrimPrefix(r.URL.Path, "/heat")
	pairs, _, err := decode(urlPath)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
	}
	key := fmt.Sprintf("%s.zip", pairs[0])
	hash := getMD5Hash(key)
	destDir := filepath.Join(LibConfig.CacheDir, hash)

	cache.mu.Lock()
	defer cache.mu.Unlock()

	item, exists := cache.files[hash]
	if exists {
		log.Println("Cache exists")
		return
	}

	log.Println(item)

	//check folder exists
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		log.Println("Cache exists", destDir)
		return
	}

	if err := downloadAndUnzip(key); err != nil {
		http.Error(w, "Failed to download and unzip", http.StatusInternalServerError)
	}

	log.Println("heatHandler", destDir)

	item = &CacheItem{
		path: filepath.Join(LibConfig.CacheDir, hash),
		cond: sync.NewCond(&sync.Mutex{}),
	}
	cache.files[hash] = item

}
