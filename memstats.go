package dziproxylib

import (
	"fmt"
	"log"
	"runtime"
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
