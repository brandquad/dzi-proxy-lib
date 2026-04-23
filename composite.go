package dziproxylib

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

var compositeLevelCache = struct {
	mu     sync.RWMutex
	levels map[string]int
}{
	levels: make(map[string]int),
}

func compositeHandler(w http.ResponseWriter, r *http.Request) {
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

	cachePath, err := ensureCompositeArchiveReady(key)
	if err != nil {
		http.Error(w, "Failed to download and unzip", http.StatusInternalServerError)
		return
	}
	debugLogMemStats("composite.after_prepare_archive")

	serveCompositeImage(w, r, key, cachePath)
}

func serveCompositeImage(w http.ResponseWriter, r *http.Request, key, cachePath string) {
	params, err := parseCompositeParams(r, key, cachePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	debugLogMemStats("composite.before_build")
	img, err := buildCompositeImage(cachePath, params)
	if err != nil {
		http.Error(w, "Failed to build image", http.StatusInternalServerError)
		return
	}
	debugLogMemStats("composite.after_build")

	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", LibConfig.HttpCacheDays*24*3600))
	w.Header().Set("Expires", time.Now().Add(time.Duration(LibConfig.HttpCacheDays)*24*time.Hour).Format(http.TimeFormat))
	w.Header().Set("Content-Type", "image/jpeg")
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: 90}); err != nil {
		http.Error(w, "Failed to encode image", http.StatusInternalServerError)
		return
	}
	debugLogMemStats("composite.after_encode")
}

func ensureCompositeArchiveReady(key string) (string, error) {
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
			path: filepath.Join(LibConfig.CacheDir, hash),
			cond: sync.NewCond(&sync.Mutex{}),
		}
		item.wg.Add(1)
		cache.files[hash] = item
		cache.mu.Unlock()

		go func() {
			defer item.wg.Done()

			if err := os.MkdirAll(item.path, os.ModePerm); err != nil {
				cache.mu.Lock()
				delete(cache.files, hash)
				cache.mu.Unlock()
				return
			}

			if err := downloadAndUnzip(key); err != nil {
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

	if _, err := os.Stat(item.path); err != nil {
		return "", err
	}

	return item.path, nil
}

func parseCompositeParams(r *http.Request, key, cachePath string) (compositeParams, error) {
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
		params.level, err = detectMaxLevel(key, cachePath)
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

func detectMaxLevel(key, cachePath string) (int, error) {
	compositeLevelCache.mu.RLock()
	level, ok := compositeLevelCache.levels[key]
	compositeLevelCache.mu.RUnlock()
	if ok {
		return level, nil
	}

	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return 0, err
	}

	maxLevel := -1
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		level, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if level > maxLevel {
			maxLevel = level
		}
	}

	if maxLevel < 0 {
		return 0, errors.New("no level directories found")
	}

	compositeLevelCache.mu.Lock()
	compositeLevelCache.levels[key] = maxLevel
	compositeLevelCache.mu.Unlock()

	return maxLevel, nil
}

func buildCompositeImage(cachePath string, params compositeParams) (image.Image, error) {
	levelDir := filepath.Join(cachePath, strconv.Itoa(params.level))
	if _, err := os.Stat(levelDir); err != nil {
		return nil, err
	}

	colWidths, err := collectColumnWidths(levelDir, params)
	if err != nil {
		return nil, err
	}
	rowHeights, err := collectRowHeights(levelDir, params)
	if err != nil {
		return nil, err
	}

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

	for row := params.rowMin; row <= params.rowMax; row++ {
		for col := params.colMin; col <= params.colMax; col++ {
			tilePath, err := findTilePath(levelDir, col, row)
			if err != nil {
				return nil, err
			}

			source, err := decodeTile(tilePath)
			if err != nil {
				return nil, err
			}

			crop := cropBounds(source.Bounds(), params, col, row)
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

			scaleTileInto(result, dstRect, source, crop, params.isColor)
		}
	}

	return result, nil
}

func collectColumnWidths(levelDir string, params compositeParams) ([]int, error) {
	widths := make([]int, 0, params.colMax-params.colMin+1)
	for col := params.colMin; col <= params.colMax; col++ {
		tilePath, err := findTilePath(levelDir, col, params.rowMin)
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

func collectRowHeights(levelDir string, params compositeParams) ([]int, error) {
	heights := make([]int, 0, params.rowMax-params.rowMin+1)
	for row := params.rowMin; row <= params.rowMax; row++ {
		tilePath, err := findTilePath(levelDir, params.colMin, row)
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

func findTilePath(levelDir string, col, row int) (string, error) {
	pattern := filepath.Join(levelDir, fmt.Sprintf("%d_%d.*", col, row))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("tile not found: %d_%d", col, row)
	}
	return matches[0], nil
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

func scaleTileInto(dst drawImage, dstRect image.Rectangle, src image.Image, srcRect image.Rectangle, isColor bool) {
	if dstRect.Empty() || srcRect.Empty() {
		return
	}

	if dstRect.Dx() == srcRect.Dx() && dstRect.Dy() == srcRect.Dy() {
		for y := 0; y < dstRect.Dy(); y++ {
			for x := 0; x < dstRect.Dx(); x++ {
				dst.Set(dstRect.Min.X+x, dstRect.Min.Y+y, normalizeColor(src.At(srcRect.Min.X+x, srcRect.Min.Y+y), isColor))
			}
		}
		return
	}

	if isColor {
		rgbaDst, ok := dst.(*image.RGBA)
		if !ok {
			return
		}
		xdraw.CatmullRom.Scale(rgbaDst, dstRect, src, srcRect, xdraw.Over, nil)
		return
	}

	grayDst, ok := dst.(*image.Gray)
	if !ok {
		return
	}
	graySrc := image.NewGray(srcRect)
	for y := srcRect.Min.Y; y < srcRect.Max.Y; y++ {
		for x := srcRect.Min.X; x < srcRect.Max.X; x++ {
			graySrc.Set(x, y, color.GrayModel.Convert(src.At(x, y)))
		}
	}
	xdraw.CatmullRom.Scale(grayDst, dstRect, graySrc, srcRect, xdraw.Over, nil)
}

func normalizeColor(c color.Color, isColor bool) color.Color {
	if isColor {
		return c
	}
	return color.GrayModel.Convert(c)
}
