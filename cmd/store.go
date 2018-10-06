package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/shirou/gopsutil/cpu"
	"github.com/spf13/cobra"
)

var s3Bucket string
var s3Client *s3.S3

func init() {
	storeCmd := &cobra.Command{
		Use:   "store [flags] [cache key] [paths...]",
		Short: "Store cache files with a key",
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			cacheKey, err := extractTemplate(args[0])
			if err != nil {
				log.Fatal(err)
			}

			exists, err := cacheExists(cacheKey)
			if err != nil {
				log.Fatal(err)
			}

			if exists {
				fmt.Printf("cache already exists: %s\n", cacheKey)
				return
			}

			paths := args[1:]
			dir, err := ioutil.TempDir("", cacheKey)
			if err != nil {
				log.Fatalf("failed to create temporal directory: %s", err)
			}

			defer os.RemoveAll(dir)

			fmt.Printf("Creating a cache: %s...\n", cacheKey)
			if err := createTar(dir, cacheKey, paths); err != nil {
				log.Fatal(err)
			}
			if err := compressGzip(dir, cacheKey); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Uploading a cache...")
			if err := uploadToS3(dir, cacheKey); err != nil {
				log.Fatal(err)
			}
		},
	}

	storeCmd.Flags().StringVarP(&s3Bucket, "s3-bucket", "", "", "S3 bucket to upload")
	storeCmd.MarkFlagRequired("s3-bucket")

	rootCmd.AddCommand(storeCmd)

	sess := session.Must(session.NewSession())
	s3Client = s3.New(sess)
}

func cacheExists(cacheKey string) (bool, error) {
	key := cacheKey + ".tar.gz"
	input := &s3.HeadObjectInput{
		Bucket: &s3Bucket,
		Key:    &key,
	}
	_, err := s3Client.HeadObject(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == "NotFound" {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func createTar(dir string, key string, paths []string) error {
	tarPath := filepath.Join(dir, key+".tar")
	tarFile, createErr := os.Create(tarPath)
	if createErr != nil {
		return fmt.Errorf("failed to create tar file: %s", createErr)
	}

	defer tarFile.Close()

	tw := tar.NewWriter(tarFile)

	defer tw.Close()

	metadataPath := filepath.Join(dir, "metadata.json")
	metadataFile, err := os.Create(metadataPath)
	if err != nil {
		return fmt.Errorf("failed to create metadata file: %s", err)
	}

	defer metadataFile.Close()

	meta := new(metadata)

	for i, path := range paths {
		meta.Paths = append(meta.Paths, path)
		childDir := fmt.Sprintf("%04d", i)
		dirwalk("", path, func(baseDir string, fileinfo os.FileInfo) error {
			targetFilePath := filepath.Join(baseDir, fileinfo.Name())
			file, fileErr := os.Open(targetFilePath)
			if fileErr != nil {
				return fmt.Errorf("failed to open: %s", fileErr)
			}

			stat, statErr := file.Stat()
			if statErr != nil {
				return fmt.Errorf("failed to get stat: %s", statErr)
			}

			var childPath string
			if filepath.IsAbs(path) {
				childPath = filepath.Join(fmt.Sprintf("%04d", i), strings.Replace(targetFilePath, filepath.Dir(path), "", 1))
			} else {
				childPath = filepath.Join(childDir, targetFilePath)
			}

			tarHeader := &tar.Header{
				Name: childPath,
				Mode: int64(stat.Mode()),
				Size: stat.Size(),
			}
			if err := tw.WriteHeader(tarHeader); err != nil {
				return fmt.Errorf("failed to write tar header: %s", err)
			}

			if _, err := io.Copy(tw, file); err != nil {
				return fmt.Errorf("failed to write file: %s", err)
			}

			return nil
		})
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("failed to flush tar file: %s", err)
	}

	metadataJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to encode metadata JSON: %s", err)
	}

	_, err = metadataFile.Write(metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to write metadata: %s", err)
	}

	tarHeader := &tar.Header{
		Name: "metadata.json",
		Mode: 0600,
		Size: int64(len(metadataJSON)),
	}
	if err := tw.WriteHeader(tarHeader); err != nil {
		return fmt.Errorf("failed to write tar header: %s", err)
	}

	if _, err := tw.Write(metadataJSON); err != nil {
		return fmt.Errorf("failed to add metadata.json to tar: %s", err)
	}

	return nil
}

func compressGzip(dir string, key string) error {
	tarPath := filepath.Join(dir, key+".tar")
	gzPath := filepath.Join(dir, key+".tar.gz")

	gzFile, gzCreateErr := os.Create(gzPath)
	if gzCreateErr != nil {
		return fmt.Errorf("failed to create gz file: %s", gzCreateErr)
	}

	defer gzFile.Close()

	tarFile, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to re-open tar file: %s", err)
	}

	defer tarFile.Close()

	gw := gzip.NewWriter(gzFile)

	defer gw.Close()

	if _, err := io.Copy(gw, tarFile); err != nil {
		return fmt.Errorf("failed to write gz: %s", err)
	}

	if err := gw.Flush(); err != nil {
		return fmt.Errorf("failed to flush gzip file: %s", err)
	}

	return nil
}

func uploadToS3(dir string, key string) error {
	gzPath := filepath.Join(dir, key+".tar.gz")
	gzFile, err := os.Open(gzPath)
	if err != nil {
		return fmt.Errorf("failed to re-open gz: %s", err)
	}

	hash := md5.New()
	if _, err := io.Copy(hash, gzFile); err != nil {
		return fmt.Errorf("failed to calculate MD5 of cache: %s", err)
	}

	base64Md5 := base64.StdEncoding.EncodeToString(hash.Sum(nil))

	gzFile.Seek(0, 0)

	gzFileStat, err := gzFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat gz: %s", err)
	}

	s3Key := key + ".tar.gz"
	size := gzFileStat.Size()
	input := &s3.PutObjectInput{
		Bucket:        &s3Bucket,
		Body:          gzFile,
		Key:           &s3Key,
		ContentLength: &size,
		ContentMD5:    &base64Md5,
	}
	if _, err := s3Client.PutObject(input); err != nil {
		return fmt.Errorf("failed to upload to S3: %s", err)
	}

	return nil
}

func dirwalk(baseDir string, target string, fn func(string, os.FileInfo) error) error {
	targetInfo, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("failed to stat file: %s", err)
	}

	if baseDir == "" {
		if targetInfo.IsDir() {
			baseDir = target
		} else {
			baseDir = filepath.Dir(target)
		}
	}

	fileinfos, err := ioutil.ReadDir(target)
	if err != nil {
		return fmt.Errorf("failed to open directory: %s", err)
	}

	for _, fileinfo := range fileinfos {
		if fileinfo.IsDir() {
			next := filepath.Join(baseDir, fileinfo.Name())
			if err := dirwalk(next, next, fn); err != nil {
				return err
			}
		} else {
			if err := fn(baseDir, fileinfo); err != nil {
				return err
			}
		}
	}

	return nil
}

var funcMap = template.FuncMap{
	"checksum": func(path string) (string, error) {
		file, err := os.Open(path)
		if err != nil {
			fmt.Println("open error")
			return "", fmt.Errorf("failed to open file: %s", err)
		}

		hash := md5.New()
		if _, err := io.Copy(hash, file); err != nil {
			return "", fmt.Errorf("failed to calculate checksum: %s", err)
		}

		return fmt.Sprintf("%x", hash.Sum(nil)), nil
	},
	"epoch": func() string {
		return strconv.Itoa(int(time.Now().Unix()))
	},
	"arch": func() (string, error) {
		info, err := cpu.Info()
		if err != nil {
			return "", fmt.Errorf("failed to get CPU info: %s", err)
		}
		if len(info) < 1 {
			return "", fmt.Errorf("zero CPU info retrieved")
		}

		return fmt.Sprintf("%s-%s-%s", runtime.GOOS, runtime.GOARCH, info[0].Model), nil
	},
}

type templateData struct {
	Environment map[string]string
}

func extractTemplate(s string) (string, error) {
	tmpl, err := template.New("cache key").Funcs(funcMap).Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid cache key: %s", err)
	}

	buf := new(bytes.Buffer)
	templateData := templateData{
		Environment: environ(),
	}
	err = tmpl.Execute(buf, templateData)
	if err != nil {
		return "", fmt.Errorf("invalid cache key: %s", err)
	}

	return buf.String(), nil
}

func environ() map[string]string {
	envMap := make(map[string]string)

	for _, env := range os.Environ() {
		keyValue := strings.SplitN(env, "=", 2)
		envMap[keyValue[0]] = keyValue[1]
	}

	return envMap
}
