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
	"sync"
	"time"
)

var cache = &ZipCache{files: make(map[string]*CacheItem)}
var fileMutexes = make(map[string]*sync.Mutex)
var fileMutexesMu sync.Mutex // защита доступа к fileMutexes
var dziPathRegex = regexp.MustCompile(`/dzi(?:_bw)?/page_\d+/([0-9a-f]+)/`)

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK) // Отвечаем "OK" на preflight-запросы
		return
	}

	// Fix problem with incorrect file path
	// Before request to proxy, filepaths are encoded in hex

	fullPath := r.URL.Path
	matches := dziPathRegex.FindStringSubmatch(fullPath)
	if len(matches) == 0 {
		return
	}
	hexedStr := matches[1]
	unHexedBytes, _ := hex.DecodeString(hexedStr)
	fullPath = strings.Replace(fullPath, hexedStr, string(unHexedBytes), 1)
	pairs := strings.Split(fullPath, ".zip")

	if len(pairs) != 2 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	//log.Println(fullPath, pairs)
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

	// Отдаем файл
	filePath := filepath.Join(LibConfig.CacheDir, hash, tilePath)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
	w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, filePath)

	if !LibConfig.Silent {
		log.Printf("Served file: %s -> %s", fullPath, filePath)
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
