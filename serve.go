package dziproxylib

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

func startCacheCleanup() {
	ttl := LibConfig.CleanupTimeout
	if ttl <= 0 && LibConfig.CleanupTimeoutCfg > 0 {
		ttl = time.Duration(LibConfig.CleanupTimeoutCfg) * time.Minute
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(ttl)
		defer ticker.Stop()
		for range ticker.C {
			cleanOldCache(ttl)
		}
	}()
}

func cleanOldCache(ttl time.Duration) {
	now := time.Now()

	cache.mu.Lock()
	var oldHashes []string
	for hash, item := range cache.files {
		if !item.lastAccess.IsZero() && now.Sub(item.lastAccess) > ttl {
			oldHashes = append(oldHashes, hash)
		}
	}
	for _, hash := range oldHashes {
		delete(cache.files, hash)
	}
	cache.mu.Unlock()

	if len(oldHashes) == 0 {
		return
	}

	fileMutexesMu.Lock()
	for _, hash := range oldHashes {
		delete(fileMutexes, hash)
	}
	fileMutexesMu.Unlock()

	compositeLevelCache.mu.Lock()
	clear(compositeLevelCache.levels)
	compositeLevelCache.mu.Unlock()

	if !LibConfig.Silent {
		log.Printf("Cleaned %d old caches", len(oldHashes))
	}
}

var cache = &ZipCache{files: make(map[string]*CacheItem)}
var fileMutexes = make(map[string]*sync.Mutex)
var fileMutexesMu sync.Mutex // защита доступа к fileMutexes
var dziPathRegex = regexp.MustCompile(`/dzi(?:_bw)?/page_\d+/([0-9a-f]+)/`)

func serveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK) // Отвечаем "OK" на preflight-запросы
		return
	}

	if strings.HasPrefix(r.URL.Path, "/heat") {
		heatHandler(w, r)
		return
	}

	pairs, fullPath, err := decode(r.URL.Path)

	if err != nil || len(pairs) != 2 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	key := fmt.Sprintf("%s.zip", pairs[0])
	tilePath := strings.TrimPrefix(pairs[1], "/")

	if key == "" || tilePath == "" {
		http.Error(w, "Missing zip or Tile-Key", http.StatusBadRequest)
		return
	}

	hash := getMD5Hash(key)

	// Локальный мьютекс для конкретного файла
	fileMutexesMu.Lock()
	mutex, exists := fileMutexes[hash]
	if !exists {
		mutex = &sync.Mutex{}
		fileMutexes[hash] = mutex
	}
	fileMutexesMu.Unlock()

	// Заблокировать доступ к конкретному файлу
	mutex.Lock()
	defer mutex.Unlock()

	// Проверяем кеш
	cache.mu.Lock()
	item, exists := cache.files[hash]
	if !exists {
		item = &CacheItem{
			path:       filepath.Join(LibConfig.CacheDir, hash),
			files:      make(map[string]string),
			tiles:      make(map[string]string),
			levels:     make(map[int]struct{}),
			levelSizes: make(map[int][]int),
			maxLevel:   -1,
			cond:       sync.NewCond(&sync.Mutex{}),
		}
		item.wg.Add(1) // Новый процесс скачивания
		cache.files[hash] = item
		cache.mu.Unlock()

		// Асинхронное скачивание и распаковка
		go func() {
			defer item.wg.Done()

			if err := prepareArchiveIndex(key, item); err != nil {
				log.Printf("Failed to prepare archive index: %v", err)
				cache.mu.Lock()
				delete(cache.files, hash)
				cache.mu.Unlock()
				return
			}
		}()
	} else {
		cache.mu.Unlock()
	}

	// Ждем завершения загрузки и распаковки
	item.wg.Wait()

	// Обновляем время последнего доступа
	cache.mu.Lock()
	item.lastAccess = time.Now()
	cache.mu.Unlock()

	filePath, ok := item.files[tilePath]
	if !ok {
		http.Error(w, "Tile not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
	w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, filePath)

	if !LibConfig.Silent {
		log.Printf("Served file: %s -> %s", fullPath, filePath)
	}
}
