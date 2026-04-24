package dziproxylib

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type compositeParams struct {
	level   int
	colMin  int
	colMax  int
	rowMin  int
	rowMax  int
	overlap int
	maxSize int
	isColor bool
}

type renderProfile struct {
	tiles      int
	findPath   time.Duration
	decodeTile time.Duration
	crop       time.Duration
	scale      time.Duration
}

var compositeLevelCache = struct {
	mu     sync.RWMutex
	levels map[string]int
}{
	levels: make(map[string]int),
}

func compositeHandler(w http.ResponseWriter, r *http.Request) {
	requestStarted := time.Now()
	defer debugLogDuration("composite.request.total", requestStarted)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	urlPath := strings.TrimPrefix(r.URL.Path, "/composite")
	pairs, _, err := decode(urlPath)
	if err != nil || len(pairs) != 2 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	key := fmt.Sprintf("%s.zip", pairs[0])
	if key == "" {
		http.Error(w, "Missing zip", http.StatusBadRequest)
		return
	}

	prepareArchiveStarted := time.Now()
	item, err := ensureCompositeArchiveReady(key)
	if err != nil {
		http.Error(w, "Failed to download and unzip", http.StatusInternalServerError)
		return
	}
	debugLogDuration("composite.prepare_archive", prepareArchiveStarted)
	debugLogMemStats("composite.after_prepare_archive")

	serveCompositeImage(w, r, key, item)
}

func serveCompositeImage(w http.ResponseWriter, r *http.Request, key string, item *CacheItem) {
	parseParamsStarted := time.Now()
	params, err := parseCompositeParams(r, key, item)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	debugLogDuration("composite.parse_params", parseParamsStarted)

	debugLogMemStats("composite.before_build")
	buildStarted := time.Now()
	img, err := buildCompositeImage(item, params)
	if err != nil {
		http.Error(w, "Failed to build image", http.StatusInternalServerError)
		return
	}
	debugLogDuration("composite.build_image", buildStarted)
	debugLogMemStats("composite.after_build")

	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
	w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
	w.Header().Set("Content-Type", "image/jpeg")
	encodeStarted := time.Now()
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: 90}); err != nil {
		http.Error(w, "Failed to encode image", http.StatusInternalServerError)
		return
	}
	debugLogDuration("composite.encode", encodeStarted)
	debugLogMemStats("composite.after_encode")
}

func ensureCompositeArchiveReady(key string) (*CacheItem, error) {
	stepStarted := time.Now()
	defer debugLogDuration("composite.ensure_archive_ready", stepStarted)

	hash := getMD5Hash(key)

	fileMutexesMu.Lock()
	mutex, exists := fileMutexes[hash]
	if !exists {
		mutex = &sync.Mutex{}
		fileMutexes[hash] = mutex
	}
	fileMutexesMu.Unlock()

	mutex.Lock()
	defer mutex.Unlock()

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
		item.wg.Add(1)
		cache.files[hash] = item
		cache.mu.Unlock()

		go func() {
			defer item.wg.Done()

			if err := prepareArchiveIndex(key, item); err != nil {
				cache.mu.Lock()
				delete(cache.files, hash)
				cache.mu.Unlock()
				return
			}
		}()
	} else {
		cache.mu.Unlock()
	}

	item.wg.Wait()

	cache.mu.Lock()
	item.lastAccess = time.Now()
	cache.mu.Unlock()

	if len(item.files) == 0 {
		return nil, errors.New("archive index is empty")
	}

	debugLogLevelSizes(hash, item.levelSizes)

	return item, nil
}

func parseCompositeParams(r *http.Request, key string, item *CacheItem) (compositeParams, error) {
	stepStarted := time.Now()
	defer debugLogDuration("composite.parse_params.inner", stepStarted)

	query := r.URL.Query()
	params := compositeParams{
		overlap: 1,
		isColor: true,
	}

	var err error
	if levelRaw := query.Get("level"); levelRaw != "" {
		if params.level, err = requiredInt(levelRaw, "level"); err != nil {
			return compositeParams{}, err
		}
	} else {
		params.level, err = detectMaxLevel(key, item)
		if err != nil {
			return compositeParams{}, err
		}
	}
	if params.colMin, err = requiredInt(query.Get("col_min"), "col_min"); err != nil {
		return compositeParams{}, err
	}
	if params.colMax, err = requiredInt(query.Get("col_max"), "col_max"); err != nil {
		return compositeParams{}, err
	}
	if params.rowMin, err = requiredInt(query.Get("row_min"), "row_min"); err != nil {
		return compositeParams{}, err
	}
	if params.rowMax, err = requiredInt(query.Get("row_max"), "row_max"); err != nil {
		return compositeParams{}, err
	}
	if overlapRaw := query.Get("overlap"); overlapRaw != "" {
		if params.overlap, err = requiredInt(overlapRaw, "overlap"); err != nil {
			return compositeParams{}, err
		}
	}

	if maxSizeRaw := query.Get("max_size"); maxSizeRaw != "" {
		params.maxSize, err = requiredInt(maxSizeRaw, "max_size")
		if err != nil {
			return compositeParams{}, err
		}
	}
	if isColorRaw := query.Get("is_color"); isColorRaw != "" {
		params.isColor, err = parseBoolParam(isColorRaw, "is_color")
		if err != nil {
			return compositeParams{}, err
		}
	}

	if params.colMin > params.colMax || params.rowMin > params.rowMax {
		return compositeParams{}, errors.New("invalid tile range")
	}
	if params.level < 0 || params.colMin < 0 || params.rowMin < 0 || params.overlap < 0 {
		return compositeParams{}, errors.New("query params must be non-negative")
	}
	if params.maxSize < 0 {
		return compositeParams{}, errors.New("max_size must be non-negative")
	}

	return params, nil
}

func detectMaxLevel(key string, item *CacheItem) (int, error) {
	stepStarted := time.Now()
	defer debugLogDuration("composite.detect_max_level", stepStarted)

	compositeLevelCache.mu.RLock()
	level, ok := compositeLevelCache.levels[key]
	compositeLevelCache.mu.RUnlock()
	if ok {
		return level, nil
	}

	if item.maxLevel < 0 {
		return 0, errors.New("no level directories found")
	}

	compositeLevelCache.mu.Lock()
	compositeLevelCache.levels[key] = item.maxLevel
	compositeLevelCache.mu.Unlock()

	return item.maxLevel, nil
}

func buildCompositeImage(item *CacheItem, params compositeParams) (image.Image, error) {
	stepStarted := time.Now()
	defer debugLogDuration("composite.build_image.total", stepStarted)

	if _, exists := item.levels[params.level]; !exists {
		return nil, fmt.Errorf("level not found: %d", params.level)
	}

	collectColumnsStarted := time.Now()
	colWidths, err := collectColumnWidths(item, params)
	if err != nil {
		return nil, err
	}
	debugLogDuration("composite.collect_column_widths", collectColumnsStarted)

	collectRowsStarted := time.Now()
	rowHeights, err := collectRowHeights(item, params)
	if err != nil {
		return nil, err
	}
	debugLogDuration("composite.collect_row_heights", collectRowsStarted)

	totalWidth := sumInts(colWidths)
	totalHeight := sumInts(rowHeights)
	if totalWidth <= 0 || totalHeight <= 0 {
		return nil, errors.New("empty image")
	}

	targetWidth, targetHeight := fitSize(totalWidth, totalHeight, params.maxSize)
	result := newCompositeCanvas(targetWidth, targetHeight, params.isColor)

	xOffsets := prefixSums(colWidths)
	yOffsets := prefixSums(rowHeights)
	scaleX := float64(targetWidth) / float64(totalWidth)
	scaleY := float64(targetHeight) / float64(totalHeight)

	renderStarted := time.Now()
	profile := renderProfile{}
	for row := params.rowMin; row <= params.rowMax; row++ {
		for col := params.colMin; col <= params.colMax; col++ {
			findPathStarted := time.Now()
			tilePath, err := findTilePath(item, params.level, col, row)
			profile.findPath += time.Since(findPathStarted)
			if err != nil {
				return nil, err
			}

			decodeStarted := time.Now()
			source, err := decodeTile(tilePath)
			profile.decodeTile += time.Since(decodeStarted)
			if err != nil {
				return nil, err
			}

			cropStarted := time.Now()
			crop := cropBounds(source.Bounds(), params, col, row)
			profile.crop += time.Since(cropStarted)
			if crop.Empty() {
				continue
			}

			srcX0 := xOffsets[col-params.colMin]
			srcX1 := xOffsets[col-params.colMin+1]
			srcY0 := yOffsets[row-params.rowMin]
			srcY1 := yOffsets[row-params.rowMin+1]

			dstRect := image.Rect(
				scaledCoord(srcX0, scaleX),
				scaledCoord(srcY0, scaleY),
				scaledCoord(srcX1, scaleX),
				scaledCoord(srcY1, scaleY),
			)
			if dstRect.Empty() {
				continue
			}

			scaleStarted := time.Now()
			scaleTileInto(result, dstRect, source, crop)
			profile.scale += time.Since(scaleStarted)
			profile.tiles++
		}
	}
	debugLogDuration("composite.render_tiles", renderStarted)
	debugLogRenderProfile(profile)

	return result, nil
}

func collectColumnWidths(item *CacheItem, params compositeParams) ([]int, error) {
	widths := make([]int, 0, params.colMax-params.colMin+1)
	for col := params.colMin; col <= params.colMax; col++ {
		tilePath, err := findTilePath(item, params.level, col, params.rowMin)
		if err != nil {
			return nil, err
		}
		cfg, err := decodeTileConfig(tilePath)
		if err != nil {
			return nil, err
		}
		width := cfg.Width
		if col > params.colMin {
			width -= params.overlap
		}
		if col < params.colMax {
			width -= params.overlap
		}
		if width <= 0 {
			return nil, fmt.Errorf("invalid width for tile %d_%d", col, params.rowMin)
		}
		widths = append(widths, width)
	}
	return widths, nil
}

func collectRowHeights(item *CacheItem, params compositeParams) ([]int, error) {
	heights := make([]int, 0, params.rowMax-params.rowMin+1)
	for row := params.rowMin; row <= params.rowMax; row++ {
		tilePath, err := findTilePath(item, params.level, params.colMin, row)
		if err != nil {
			return nil, err
		}
		cfg, err := decodeTileConfig(tilePath)
		if err != nil {
			return nil, err
		}
		height := cfg.Height
		if row > params.rowMin {
			height -= params.overlap
		}
		if row < params.rowMax {
			height -= params.overlap
		}
		if height <= 0 {
			return nil, fmt.Errorf("invalid height for tile %d_%d", params.colMin, row)
		}
		heights = append(heights, height)
	}
	return heights, nil
}

func cropBounds(bounds image.Rectangle, params compositeParams, col, row int) image.Rectangle {
	left := 0
	right := 0
	top := 0
	bottom := 0

	if col > params.colMin {
		left = params.overlap
	}
	if col < params.colMax {
		right = params.overlap
	}
	if row > params.rowMin {
		top = params.overlap
	}
	if row < params.rowMax {
		bottom = params.overlap
	}

	minX := bounds.Min.X + min(left, bounds.Dx())
	minY := bounds.Min.Y + min(top, bounds.Dy())
	maxX := bounds.Max.X - min(right, bounds.Dx())
	maxY := bounds.Max.Y - min(bottom, bounds.Dy())

	if maxX < minX {
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}

	return image.Rect(minX, minY, maxX, maxY)
}

func fitSize(width, height, maxSize int) (int, int) {
	if maxSize <= 0 {
		return width, height
	}

	if width >= height {
		if width <= maxSize {
			return width, height
		}
		targetWidth := maxSize
		targetHeight := max(1, int(math.Round(float64(height)*float64(maxSize)/float64(width))))
		return targetWidth, targetHeight
	}

	if height <= maxSize {
		return width, height
	}

	targetHeight := maxSize
	targetWidth := max(1, int(math.Round(float64(width)*float64(maxSize)/float64(height))))
	return targetWidth, targetHeight
}

func findTilePath(item *CacheItem, level, col, row int) (string, error) {
	tileKey := fmt.Sprintf("%d/%d_%d", level, col, row)
	if path, ok := item.tiles[tileKey]; ok {
		return path, nil
	}
	return "", fmt.Errorf("tile not found: %d/%d_%d", level, col, row)
}

func decodeTileConfig(path string) (image.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer file.Close()

	cfg, _, err := image.DecodeConfig(file)
	return cfg, err
}

func decodeTile(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	return img, err
}

func prefixSums(values []int) []int {
	out := make([]int, len(values)+1)
	for i, value := range values {
		out[i+1] = out[i] + value
	}
	return out
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func scaledCoord(value int, scale float64) int {
	return int(math.Round(float64(value) * scale))
}

func newCompositeCanvas(width, height int, isColor bool) drawImage {
	if isColor {
		return image.NewRGBA(image.Rect(0, 0, width, height))
	}
	return image.NewGray(image.Rect(0, 0, width, height))
}

type drawImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func scaleTileInto(dst drawImage, dstRect image.Rectangle, src image.Image, srcRect image.Rectangle) {
	if dstRect.Empty() || srcRect.Empty() {
		return
	}

	if dstRect.Dx() == srcRect.Dx() && dstRect.Dy() == srcRect.Dy() {
		for y := 0; y < dstRect.Dy(); y++ {
			for x := 0; x < dstRect.Dx(); x++ {
				dst.Set(dstRect.Min.X+x, dstRect.Min.Y+y, src.At(srcRect.Min.X+x, srcRect.Min.Y+y))
			}
		}
		return
	}

	drawDst, ok := dst.(stddraw.Image)
	if !ok {
		return
	}
	xdraw.ApproxBiLinear.Scale(drawDst, dstRect, src, srcRect, xdraw.Over, nil)
}
