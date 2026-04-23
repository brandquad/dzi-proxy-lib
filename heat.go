package dziproxylib

import (
	"fmt"
	"log"
	"net/http"
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
	item = &CacheItem{
		path:       filepath.Join(LibConfig.CacheDir, hash),
		files:      make(map[string]string),
		tiles:      make(map[string]string),
		levels:     make(map[int]struct{}),
		levelSizes: make(map[int][]int),
		maxLevel:   -1,
		cond:       sync.NewCond(&sync.Mutex{}),
	}

	if err := prepareArchiveIndex(key, item); err != nil {
		http.Error(w, "Failed to download and unzip", http.StatusInternalServerError)
		return
	}
	cache.files[hash] = item

	log.Println("heatHandler", destDir)

}
