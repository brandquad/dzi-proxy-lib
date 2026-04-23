package dziproxylib

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
	files        map[string]string
	tiles        map[string]string
	levels       map[int]struct{}
	levelSizes   map[int][]int
	maxLevel     int
	lastAccess   time.Time
	isExtracting bool // Можно удалить, если используется WaitGroup
	cond         *sync.Cond
	wg           sync.WaitGroup // Группа ожидания для завершения работы горутины
}
