package dziproxylib

import (
	"archive/zip"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
)

type archiveIndex struct {
	files      map[string]string
	tiles      map[string]string
	levels     map[int]struct{}
	levelSizes map[int][]int
	maxLevel   int
}

func newArchiveIndex() *archiveIndex {
	return &archiveIndex{
		files:      make(map[string]string),
		tiles:      make(map[string]string),
		levels:     make(map[int]struct{}),
		levelSizes: make(map[int][]int),
		maxLevel:   -1,
	}
}

func parseBoolParam(raw, name string) (bool, error) {
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func requiredInt(raw, name string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("missing %s", name)
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", name)
	}

	return value, nil
}

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

func getMD5Hash(text string) string {
	hasher := md5.Sum([]byte(text))
	return hex.EncodeToString(hasher[:])
}

func prepareArchiveIndex(key string, item *CacheItem) error {
	if stat, err := os.Stat(item.path); err == nil && stat.IsDir() {
		index, err := loadArchiveIndexFromDir(item.path)
		if err != nil {
			return err
		}
		applyArchiveIndex(item, index)
		return nil
	}

	if err := os.MkdirAll(item.path, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	index, err := downloadAndUnzip(key)
	if err != nil {
		return err
	}

	applyArchiveIndex(item, index)
	return nil
}

func applyArchiveIndex(item *CacheItem, index *archiveIndex) {
	item.files = index.files
	item.tiles = index.tiles
	item.levels = index.levels
	item.levelSizes = index.levelSizes
	item.maxLevel = index.maxLevel
}

func downloadAndUnzip(key string) (*archiveIndex, error) {

	log.Println("[D] Downloading s3 archive", key)

	// Скачиваем ZIP-файл из s3Client
	hashStr := getMD5Hash(key)
	destDir := filepath.Join(LibConfig.CacheDir, hashStr)

	zipFilePath := filepath.Join(LibConfig.CacheDir, hashStr+".zip")
	defer os.Remove(zipFilePath) // Удаляем временный файл после использования

	file, err := os.Create(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer file.Close()

	// Запрос к s3Client
	input := &s3.GetObjectInput{
		Bucket: aws.String(LibConfig.S3Bucket),
		Key:    aws.String(key),
	}

	var s3Client = getS3()

	output, err := s3Client.GetObject(input)
	if err != nil {
		return nil, fmt.Errorf("failed to download file from s3Client: %w", err)
	}
	defer output.Body.Close()

	// Записываем содержимое в файл
	_, err = io.Copy(file, output.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	// Распаковываем ZIP-файл
	index, err := unzip(zipFilePath, destDir)
	if err != nil {
		return nil, fmt.Errorf("failed to unzip file: %w", err)
	}

	if err := populateLevelSizes(index); err != nil {
		return nil, fmt.Errorf("failed to populate level sizes: %w", err)
	}

	return index, nil
}

func loadArchiveIndexFromDir(root string) (*archiveIndex, error) {
	index := newArchiveIndex()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		index.files[relativePath] = path

		pathParts := strings.Split(relativePath, "/")
		if len(pathParts) == 2 {
			level, err := strconv.Atoi(pathParts[0])
			if err == nil {
				index.levels[level] = struct{}{}
				if level > index.maxLevel {
					index.maxLevel = level
				}
				tileKey := fmt.Sprintf("%d/%s", level, strings.TrimSuffix(pathParts[1], filepath.Ext(pathParts[1])))
				index.tiles[tileKey] = path
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := populateLevelSizes(index); err != nil {
		return nil, err
	}

	return index, nil
}

func populateLevelSizes(index *archiveIndex) error {
	type levelDims struct {
		colPaths map[int]string
		rowPaths map[int]string
	}

	levelMeta := make(map[int]*levelDims)
	for tileKey, tilePath := range index.tiles {
		parts := strings.Split(tileKey, "/")
		if len(parts) != 2 {
			continue
		}

		level, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}

		coords := strings.Split(parts[1], "_")
		if len(coords) != 2 {
			continue
		}

		col, err := strconv.Atoi(coords[0])
		if err != nil {
			continue
		}
		row, err := strconv.Atoi(coords[1])
		if err != nil {
			continue
		}

		meta, ok := levelMeta[level]
		if !ok {
			meta = &levelDims{
				colPaths: make(map[int]string),
				rowPaths: make(map[int]string),
			}
			levelMeta[level] = meta
		}

		if _, exists := meta.colPaths[col]; !exists {
			meta.colPaths[col] = tilePath
		}
		if _, exists := meta.rowPaths[row]; !exists {
			meta.rowPaths[row] = tilePath
		}
	}

	for level, meta := range levelMeta {
		width, err := sumTileConfigs(meta.colPaths, true)
		if err != nil {
			return err
		}
		height, err := sumTileConfigs(meta.rowPaths, false)
		if err != nil {
			return err
		}
		index.levelSizes[level] = []int{width, height}
	}

	return nil
}

func sumTileConfigs(paths map[int]string, useWidth bool) (int, error) {
	keys := make([]int, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	total := 0
	for _, key := range keys {
		width, height, err := loadVipsTileSize(paths[key])
		if err != nil {
			return 0, err
		}
		if useWidth {
			total += width
			continue
		}
		total += height
	}

	return total, nil
}

func unzip(src, dest string) (*archiveIndex, error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	index := newArchiveIndex()

	for _, f := range r.File {

		if !f.FileInfo().IsDir() && (strings.HasSuffix(f.Name, ".dzi") || strings.HasSuffix(f.Name, ".xml")) {
			continue
		}

		//log.Println(f.Name)
		n := strings.Split(f.Name, "/")
		var fPath string
		switch {
		case len(n) == 3:
			fPath = filepath.Join(dest, n[1], n[2])
		case len(n) == 4:
			fPath = filepath.Join(dest, n[2], n[3])
		default:
			return nil, fmt.Errorf("illegal file path: %s", fPath)
		}

		if !strings.HasPrefix(fPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return nil, fmt.Errorf("illegal file path: %s", fPath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fPath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fPath), os.ModePerm); err != nil {
			return nil, err
		}

		var (
			outFile *os.File
			rc      io.ReadCloser
		)

		outFile, err = os.OpenFile(fPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return nil, err
		}

		rc, err = f.Open()
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return nil, err
		}

		relativePath, err := filepath.Rel(dest, fPath)
		if err != nil {
			return nil, err
		}
		relativePath = filepath.ToSlash(relativePath)
		index.files[relativePath] = fPath

		pathParts := strings.Split(relativePath, "/")
		if len(pathParts) == 2 {
			level, err := strconv.Atoi(pathParts[0])
			if err == nil {
				index.levels[level] = struct{}{}
				if level > index.maxLevel {
					index.maxLevel = level
				}
				tileKey := fmt.Sprintf("%d/%s", level, strings.TrimSuffix(pathParts[1], filepath.Ext(pathParts[1])))
				index.tiles[tileKey] = fPath
			}
		}
	}
	return index, nil
}
