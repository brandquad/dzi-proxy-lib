package dziproxylib

import (
	"archive/zip"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func getMD5Hash(text string) string {
	hasher := md5.Sum([]byte(text))
	return hex.EncodeToString(hasher[:])
}

func downloadAndUnzip(key string) error {

	log.Println("[D] Downloading s3 archive", key)

	// Скачиваем ZIP-файл из s3Client
	hashStr := getMD5Hash(key)
	destDir := filepath.Join(LibConfig.CacheDir, hashStr)

	zipFilePath := filepath.Join(LibConfig.CacheDir, hashStr+".zip")
	defer os.Remove(zipFilePath) // Удаляем временный файл после использования

	file, err := os.Create(zipFilePath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
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
		return fmt.Errorf("failed to download file from s3Client: %w", err)
	}
	defer output.Body.Close()

	// Записываем содержимое в файл
	_, err = io.Copy(file, output.Body)
	if err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}

	// Распаковываем ZIP-файл
	err = unzip(zipFilePath, destDir)
	if err != nil {
		return fmt.Errorf("failed to unzip file: %w", err)
	}

	return nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

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
			return fmt.Errorf("illegal file path: %s", fPath)
		}

		if !strings.HasPrefix(fPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fPath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fPath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fPath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}
