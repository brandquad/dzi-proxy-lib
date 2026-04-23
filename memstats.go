package dziproxylib

import (
	"fmt"
	"log"
	"runtime"
	"slices"
	"time"
)

func logMemStats(stage string) string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return fmt.Sprintf(
		"mem %s: alloc=%dMB heap_inuse=%dMB heap_sys=%dMB num_gc=%d goroutines=%d",
		stage,
		mem.Alloc/1024/1024,
		mem.HeapInuse/1024/1024,
		mem.HeapSys/1024/1024,
		mem.NumGC,
		runtime.NumGoroutine(),
	)
}

func debugLogMemStats(stage string) {
	if LibConfig == nil || !LibConfig.Debug {
		return
	}
	log.Println(logMemStats(stage))
}

func debugLogDuration(stage string, started time.Time) {
	if LibConfig == nil || !LibConfig.Debug {
		return
	}
	log.Printf("[perf] %s: %s", stage, time.Since(started))
}

func debugLogRenderProfile(profile renderProfile) {
	if LibConfig == nil || !LibConfig.Debug {
		return
	}
	log.Printf(
		"[perf] composite.render_tiles.details: tiles=%d find_path=%s decode_tile=%s crop=%s scale=%s",
		profile.tiles,
		profile.findPath,
		profile.decodeTile,
		profile.crop,
		profile.scale,
	)
}

func debugLogLevelSizes(hash string, levelSizes map[int][]int) {
	if LibConfig == nil || !LibConfig.Debug {
		return
	}

	levels := make([]int, 0, len(levelSizes))
	for level := range levelSizes {
		levels = append(levels, level)
	}
	slices.Sort(levels)

	ordered := make(map[int][]int, len(levels))
	for _, level := range levels {
		ordered[level] = levelSizes[level]
	}

	log.Printf("[cache] level_sizes %s: %+v", hash, ordered)
}
