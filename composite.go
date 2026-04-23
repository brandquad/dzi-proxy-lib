package dziproxylib

import (
	"errors"
	"fmt"
	"image"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
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

var emptyCompositeImageJPEG = []byte{
	0xff, 0xd8, 0xff, 0xdb, 0x0, 0x84, 0x0, 0x5, 0x3, 0x4, 0x4, 0x4, 0x3, 0x5, 0x4, 0x4,
	0x4, 0x5, 0x5, 0x5, 0x6, 0x7, 0xc, 0x8, 0x7, 0x7, 0x7, 0x7, 0xf, 0xb, 0xb, 0x9,
	0xc, 0x11, 0xf, 0x12, 0x12, 0x11, 0xf, 0x11, 0x11, 0x13, 0x16, 0x1c, 0x17, 0x13, 0x14, 0x1a,
	0x15, 0x11, 0x11, 0x18, 0x21, 0x18, 0x1a, 0x1d, 0x1d, 0x1f, 0x1f, 0x1f, 0x13, 0x17, 0x22, 0x24,
	0x22, 0x1e, 0x24, 0x1c, 0x1e, 0x1f, 0x1e, 0x1, 0x5, 0x5, 0x5, 0x7, 0x6, 0x7, 0xe, 0x8,
	0x8, 0xe, 0x1e, 0x14, 0x11, 0x14, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e,
	0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e,
	0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e,
	0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0x1e, 0xff, 0xc0, 0x0, 0x11, 0x8, 0x0, 0x1, 0x0, 0x1, 0x3,
	0x1, 0x22, 0x0, 0x2, 0x11, 0x1, 0x3, 0x11, 0x1, 0xff, 0xc4, 0x1, 0xa2, 0x0, 0x0, 0x1, 0x5,
	0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x1, 0x2,
	0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0x10, 0x0, 0x2, 0x1, 0x3, 0x3, 0x2,
	0x4, 0x3, 0x5, 0x5, 0x4, 0x4, 0x0, 0x0, 0x1, 0x7d, 0x1, 0x2, 0x3, 0x0, 0x4, 0x11,
	0x5, 0x12, 0x21, 0x31, 0x41, 0x6, 0x13, 0x51, 0x61, 0x7, 0x22, 0x71, 0x14, 0x32, 0x81, 0x91,
	0xa1, 0x8, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x9,
	0xa, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36, 0x37,
	0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57,
	0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77,
	0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96,
	0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4,
	0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2,
	0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8,
	0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0x1, 0x0, 0x3, 0x1,
	0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x1, 0x2,
	0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0x11, 0x0, 0x2, 0x1, 0x2, 0x4, 0x4,
	0x3, 0x4, 0x7, 0x5, 0x4, 0x4, 0x0, 0x1, 0x2, 0x77, 0x0, 0x1, 0x2, 0x3, 0x11, 0x4,
	0x5, 0x21, 0x31, 0x6, 0x12, 0x41, 0x51, 0x7, 0x61, 0x71, 0x13, 0x22, 0x32, 0x81, 0x8, 0x14,
	0x42, 0x91, 0xa1, 0xb1, 0xc1, 0x9, 0x23, 0x33, 0x52, 0xf0, 0x15, 0x62, 0x72, 0xd1, 0xa, 0x16,
	0x24, 0x34, 0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x35, 0x36,
	0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x53, 0x54, 0x55, 0x56,
	0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76,
	0x77, 0x78, 0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94,
	0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2,
	0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9,
	0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
	0xe8, 0xe9, 0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9, 0xfa, 0xff, 0xda, 0x0, 0xc,
	0x3, 0x1, 0x0, 0x2, 0x11, 0x3, 0x11, 0x0, 0x3f, 0x0, 0xf8, 0xca, 0x8a, 0x28, 0xa0, 0xf,
	0xff, 0xd9,
}

var vipsStartupOnce sync.Once
var vipsStartupErr error

func ensureVipsStarted() error {
	vipsStartupOnce.Do(func() {
		vips.LoggingSettings(func(messageDomain string, messageLevel vips.LogLevel, message string) {}, vips.LogLevelError)
		vipsStartupErr = vips.Startup(nil)
	})
	return vipsStartupErr
}

func compositeHandler(w http.ResponseWriter, r *http.Request) {
	requestStarted := time.Now()
	defer debugLogDuration("composite.request.total", requestStarted)

	if err := ensureVipsStarted(); err != nil {
		http.Error(w, "Failed to initialize libvips", http.StatusInternalServerError)
		return
	}

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
	imageBytes, err := buildCompositeImage(item, params)
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
	if _, err := w.Write(imageBytes); err != nil {
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

func buildCompositeImage(item *CacheItem, params compositeParams) ([]byte, error) {
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
	canvas, err := newVipsCanvas(targetWidth, targetHeight)
	if err != nil {
		return nil, err
	}
	defer canvas.Close()

	xOffsets := prefixSums(colWidths)
	yOffsets := prefixSums(rowHeights)
	scaleX := float64(targetWidth) / float64(totalWidth)
	scaleY := float64(targetHeight) / float64(totalHeight)

	renderStarted := time.Now()
	profile := renderProfile{}
	overlays := make([]*vips.ImageComposite, 0, (params.rowMax-params.rowMin+1)*(params.colMax-params.colMin+1))
	defer closeComposites(overlays)

	for row := params.rowMin; row <= params.rowMax; row++ {
		for col := params.colMin; col <= params.colMax; col++ {
			findPathStarted := time.Now()
			tilePath, err := findTilePath(item, params.level, col, row)
			profile.findPath += time.Since(findPathStarted)
			if err != nil {
				return nil, err
			}

			decodeStarted := time.Now()
			tile, err := vips.LoadImageFromFile(tilePath, nil)
			profile.decodeTile += time.Since(decodeStarted)
			if err != nil {
				return nil, err
			}

			cropStarted := time.Now()
			crop := cropBounds(image.Rect(0, 0, tile.Width(), tile.Height()), params, col, row)
			profile.crop += time.Since(cropStarted)
			if crop.Empty() {
				tile.Close()
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
				tile.Close()
				continue
			}

			scaleStarted := time.Now()
			if err := tile.ExtractArea(crop.Min.X, crop.Min.Y, crop.Dx(), crop.Dy()); err != nil {
				tile.Close()
				return nil, err
			}
			if tile.Width() != dstRect.Dx() || tile.Height() != dstRect.Dy() {
				if err := tile.ResizeWithVScale(
					float64(dstRect.Dx())/float64(tile.Width()),
					float64(dstRect.Dy())/float64(tile.Height()),
					vips.KernelLinear,
				); err != nil {
					tile.Close()
					return nil, err
				}
			}
			if err := tile.ToColorSpace(vips.InterpretationSRGB); err != nil {
				tile.Close()
				return nil, err
			}
			profile.scale += time.Since(scaleStarted)

			overlays = append(overlays, &vips.ImageComposite{
				Image:     tile,
				BlendMode: vips.BlendModeOver,
				X:         dstRect.Min.X,
				Y:         dstRect.Min.Y,
			})
			profile.tiles++
		}
	}

	if len(overlays) > 0 {
		if err := canvas.CompositeMulti(overlays); err != nil {
			return nil, err
		}
	}
	debugLogDuration("composite.render_tiles", renderStarted)
	debugLogRenderProfile(profile)
	debugLogMemStats("composite.after_render_tiles")

	flattenStarted := time.Now()
	if err := canvas.Flatten(&vips.Color{R: 0, G: 0, B: 0}); err != nil {
		return nil, err
	}
	debugLogDuration("composite.flatten", flattenStarted)
	debugLogMemStats("composite.after_flatten")

	avg, err := calcAvgGrayImage(canvas)
	if err != nil {
		return nil, err
	}
	if avg > 220 || avg < 30 {
		return emptyCompositeImageJPEG, nil
	}

	if !params.isColor {
		colorSpaceStarted := time.Now()
		if err := canvas.ToColorSpace(vips.InterpretationBW); err != nil {
			return nil, err
		}
		debugLogDuration("composite.to_bw", colorSpaceStarted)
		debugLogMemStats("composite.after_to_bw")
	}

	jpegParams := &vips.JpegExportParams{
		Quality:            85,                          // 95 → 85 даёт ~2x прирост и почти незаметно глазу
		Interlace:          false,                       // progressive JPEG медленнее в 1.5-2 раза
		OptimizeCoding:     false,                       // Huffman-оптимизация дорогая, выигрыш ~3-5% размера
		SubsampleMode:      vips.VipsForeignSubsampleOn, // 4:2:0 вместо 4:4:4
		TrellisQuant:       false,                       // все эти "optimize"-флаги жрут CPU
		OvershootDeringing: false,
		OptimizeScans:      false,
		QuantTable:         0,
	}

	exportStarted := time.Now()
	imageBytes, _, err := canvas.ExportJpeg(jpegParams)
	if err != nil {
		return nil, err
	}
	debugLogDuration("composite.export_jpeg", exportStarted)
	debugLogMemStats("composite.after_export_jpeg")

	return imageBytes, nil
}

func newVipsCanvas(width, height int) (*vips.ImageRef, error) {
	canvas, err := vips.NewTransparentCanvas(width, height)
	if err != nil {
		return nil, err
	}
	return canvas, nil
}

func calcAvgGrayImage(img *vips.ImageRef) (uint8, error) {
	if img == nil {
		return 0, nil
	}

	grayImage, err := img.Copy()
	if err != nil {
		return 0, err
	}
	defer grayImage.Close()

	if err := grayImage.ToColorSpace(vips.InterpretationBW); err != nil {
		return 0, err
	}

	avg, err := grayImage.Average()
	if err != nil {
		return 0, err
	}

	if avg < 0 {
		avg = 0
	}
	if avg > 255 {
		avg = 255
	}

	return uint8(avg + 0.5), nil
}

func closeComposites(composites []*vips.ImageComposite) {
	for _, composite := range composites {
		if composite != nil && composite.Image != nil {
			composite.Image.Close()
		}
	}
}

func collectColumnWidths(item *CacheItem, params compositeParams) ([]int, error) {
	widths := make([]int, 0, params.colMax-params.colMin+1)
	for col := params.colMin; col <= params.colMax; col++ {
		tilePath, err := findTilePath(item, params.level, col, params.rowMin)
		if err != nil {
			return nil, err
		}
		width, _, err := loadVipsTileSize(tilePath)
		if err != nil {
			return nil, err
		}
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
		_, height, err := loadVipsTileSize(tilePath)
		if err != nil {
			return nil, err
		}
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

func loadVipsTileSize(path string) (int, int, error) {
	ref, err := vips.LoadImageFromFile(path, nil)
	if err != nil {
		return 0, 0, err
	}
	defer ref.Close()
	return ref.Width(), ref.Height(), nil
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
		targetHeight := max(1, int(float64(height)*float64(maxSize)/float64(width)+0.5))
		return targetWidth, targetHeight
	}

	if height <= maxSize {
		return width, height
	}

	targetHeight := maxSize
	targetWidth := max(1, int(float64(width)*float64(maxSize)/float64(height)+0.5))
	return targetWidth, targetHeight
}

func findTilePath(item *CacheItem, level, col, row int) (string, error) {
	tileKey := fmt.Sprintf("%d/%d_%d", level, col, row)
	if path, ok := item.tiles[tileKey]; ok {
		return path, nil
	}
	return "", fmt.Errorf("tile not found: %d/%d_%d", level, col, row)
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
	return int(float64(value)*scale + 0.5)
}
