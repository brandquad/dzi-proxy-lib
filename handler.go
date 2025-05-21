package dziproxylib

import (
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"bytes"
	"sync"
	"time"
)

var cache = &ZipCache{files: make(map[string]*CacheItem)}
// var fileMutexes = make(map[string]*sync.Mutex) // Removed
// var fileMutexesMu sync.Mutex // защита доступа к fileMutexes // Removed
var fileMutexes = &sync.Map{} // New sync.Map for file-specific mutexes
var dziPathRegex = regexp.MustCompile(`/dzi(?:_bw)?/page_\d+/([0-9a-f]+)/`)

func decode(urlPath string) ([]string, string, error) {
	fullPath := urlPath
	matches := dziPathRegex.FindStringSubmatch(fullPath)
	if len(matches) == 0 {
		return nil, "", nil
	}
	hexedStr := matches[1]
	unHexedBytes, err := hex.DecodeString(hexedStr)
	if err != nil {
		return nil, "", err
	}
	fullPath = strings.Replace(fullPath, hexedStr, string(unHexedBytes), 1)
	pairs := strings.Split(fullPath, ".zip")

	return pairs, fullPath, nil
}

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

func handler(w http.ResponseWriter, r *http.Request) {
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

	// Локальный мьютекс для конкретного файла using sync.Map
	var mutex *sync.Mutex
	rawMutex, _ := fileMutexes.LoadOrStore(hash, &sync.Mutex{})
	mutex = rawMutex.(*sync.Mutex)

	// Заблокировать доступ к конкретному файлу
	mutex.Lock()
	defer mutex.Unlock()

	// Проверяем кеш
	cache.mu.Lock()
	item, exists := cache.files[hash]
	if !exists {
		item = &CacheItem{
			path: filepath.Join(LibConfig.CacheDir, hash),
			cond: sync.NewCond(&sync.Mutex{}),
		}
		item.wg.Add(1) // Новый процесс скачивания
		cache.files[hash] = item
		cache.mu.Unlock()

		// Асинхронное скачивание и распаковка
		go func() {
			defer item.wg.Done()

			// Создаем директорию
			if err := os.MkdirAll(item.path, os.ModePerm); err != nil {
				log.Printf("Failed to create cache directory: %v", err)
				cache.mu.Lock()
				delete(cache.files, hash)
				cache.mu.Unlock()
				return
			}

			// Скачиваем и распаковываем ZIP
			if err := downloadAndUnzip(key); err != nil {
				log.Printf("Failed to download or extract ZIP file: %v", err)
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

	// In-memory cache key
	memCacheKey := hash + "/" + tilePath

	// 1. Check In-Memory Cache
	if tileData, found := memCache.Get(memCacheKey); found {
		w.Header().Set("Content-Type", mimeTypeByExtension(filepath.Ext(tilePath)))
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
		w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
		if !LibConfig.Silent {
			log.Printf("Served from memory cache: %s -> %s", fullPath, memCacheKey)
		}
		http.ServeContent(w, r, tilePath, time.Now(), bytes.NewReader(tileData))
		return
	}

	// Отдаем файл
	filePath := filepath.Join(LibConfig.CacheDir, hash, tilePath)

	// 2. Populate In-Memory Cache (after disk operations, before serving)
	// Read the file content to potentially store in memory cache and serve directly
	tileData, err := os.ReadFile(filePath)
	if err == nil {
		memCache.Put(memCacheKey, tileData) // Add to in-memory cache
		if !LibConfig.Silent {
			log.Printf("Populated memory cache and serving: %s -> %s", fullPath, memCacheKey)
		}
		w.Header().Set("Content-Type", mimeTypeByExtension(filepath.Ext(tilePath)))
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
		w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
		http.ServeContent(w, r, tilePath, time.Now(), bytes.NewReader(tileData))
	} else {
		// Fallback to http.ServeFile if reading for memory cache failed
		if !LibConfig.Silent {
			log.Printf("Failed to read file for memory cache, falling back to ServeFile: %s. Error: %v", filePath, err)
		}
		w.Header().Set("Content-Type", mimeTypeByExtension(filepath.Ext(tilePath))) // Ensure content type is set
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
		w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
		http.ServeFile(w, r, filePath)
	}

	if !LibConfig.Silent {
		// This log might be redundant if served from memory, but good for ServeFile case
		log.Printf("Served file (potentially after fallback): %s -> %s", fullPath, filePath)
	}
}

// Фоновая горутина для очистки устаревших директорий
func CleanupCache() {
	for {
		time.Sleep(1 * time.Minute)
		cache.mu.Lock()
		for zipPath, item := range cache.files {
			if time.Since(item.lastAccess) > LibConfig.CleanupTimeout {
				os.RemoveAll(filepath.Join(LibConfig.CacheDir, filepath.Base(zipPath)))
				delete(cache.files, zipPath)
			}
		}
		cache.mu.Unlock()
	}
}

// mimeTypeByExtension returns a basic MIME type based on file extension.
func mimeTypeByExtension(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".jpeg", ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".dzi": // Deep Zoom Image typically uses XML format for descriptors
		return "application/xml"
	case ".xml":
		return "application/xml"
	case ".json":
		return "application/json"
	default:
		// log.Printf("[W] Unknown extension '%s', serving as application/octet-stream", ext)
		return "application/octet-stream"
	}
}
