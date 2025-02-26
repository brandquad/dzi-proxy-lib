package dziproxy

import (
	"sync"
	"time"
)

type ZipCache struct {
	mu    sync.Mutex
	files map[string]*CacheItem
}

type CacheItem struct {
	path         string
	lastAccess   time.Time
	isExtracting bool // Можно удалить, если используется WaitGroup
	cond         *sync.Cond
	wg           sync.WaitGroup // Группа ожидания для завершения работы горутины
}
