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
	"bytes"
	"bytes"
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

	// Запрос к s3Client
	input := &s3.GetObjectInput{
		Bucket: aws.String(LibConfig.S3Bucket),
		Key:    aws.String(key),
	}

	var s3Client = getS3()

	output, err := s3Client.GetObject(input)
	if err != nil {
		return fmt.Errorf("failed to download file from S3: %w", err)
	}
	defer output.Body.Close()

	bodyBytes, err := io.ReadAll(output.Body)
	if err != nil {
		return fmt.Errorf("failed to read S3 object body into memory: %w", err)
	}

	var size int64
	if output.ContentLength != nil {
		size = *output.ContentLength
	} else {
		// This case should ideally not happen for S3 objects, but good to be aware.
		// If it does, using len(bodyBytes) is a fallback.
		log.Println("[W] S3 ContentLength is nil, using len(bodyBytes) for zip reader size.")
		size = int64(len(bodyBytes))
	}

	reader := bytes.NewReader(bodyBytes)

	// Распаковываем ZIP-файл из памяти
	err = unzip(reader, size, destDir)
	if err != nil {
		return fmt.Errorf("failed to unzip data from memory: %w", err)
	}

	log.Printf("[D] Successfully downloaded and unzipped %s to %s from memory", key, destDir)
	return nil
}

func unzip(zipReader io.ReaderAt, size int64, dest string) error {
	r, err := zip.NewReader(zipReader, size)
	if err != nil {
		return fmt.Errorf("failed to read zip archive from memory: %w", err)
	}

	for _, f := range r.File {
		// Skip .dzi or .xml files that are not directories themselves.
		// The original check was:
		// if !f.FileInfo().IsDir() && (strings.HasSuffix(f.Name, ".dzi") || strings.HasSuffix(f.Name, ".xml")) {
		//  continue
		// }
		// We need to ensure this logic is correctly applied based on f.Name (the full path within the zip)
		// and not fPath (the path on the destination file system).
		
		if !f.FileInfo().IsDir() {
			// f.Name is the full path within the zip. Check its suffix.
			if strings.HasSuffix(strings.ToLower(f.Name), ".dzi") || strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
				log.Printf("Skipping DZI/XML file: %s", f.Name) // Optional: log the skip
				continue
			}
		}
		
		// Construct the full path for the file/directory to be created
		fPath := filepath.Join(dest, f.Name)

		// Path traversal check (ensure files are written within dest)
		if !strings.HasPrefix(fPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			// log.Printf("[D] Creating directory: %s", fPath)
			if err := os.MkdirAll(fPath, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", fPath, err)
			}
			continue
		}

		// Create parent directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(fPath), os.ModePerm); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", fPath, err)
		}

		// Open the destination file
		outFile, err := os.OpenFile(fPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return fmt.Errorf("failed to open destination file %s: %w", fPath, err)
		}

		// Open the source file from zip
		rc, err := f.Open()
		if err != nil {
			outFile.Close() // Close outFile if f.Open() fails
			return fmt.Errorf("failed to open file in zip %s: %w", f.Name, err)
		}

		// Copy the file content
		_, err = io.Copy(outFile, rc)

		// Close both files
		rc.Close()   // Close rc first
		outErr := outFile.Close() // Then close outFile and capture its error

		if err != nil { // Error during io.Copy
			return fmt.Errorf("failed to copy content for %s: %w", f.Name, err)
		}
		if outErr != nil { // Error during outFile.Close()
			return fmt.Errorf("failed to close destination file %s: %w", fPath, outErr)
		}
		// log.Printf("[D] Successfully unzipped file: %s", fPath)
	}
	return nil
}
