package cmd

import (
	"archive/tar"
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
	"strings"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/spf13/cobra"
	"github.com/yuya-takeyama/guruguru-cache/template"
)

var s3Bucket string
var s3Client *s3.S3

func init() {
	storeCmd := &cobra.Command{
		Use:   "store [flags] [cache key] [paths...]",
		Short: "Store cache files with a key",
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			cacheKey, err := template.ExecuteTemplate(args[0])
			if err != nil {
				log.Fatal(err)
			}

			exists, err := cacheExists(cacheKey)
			if err != nil {
				log.Fatal(err)
			}

			if exists {
				log.Printf("cache already exists: %s\n", cacheKey)
				return
			}

			paths := args[1:]
			dir, err := ioutil.TempDir("", cacheKey)
			if err != nil {
				log.Fatalf("failed to create temporal directory: %s", err)
			}

			defer os.RemoveAll(dir)

			log.Printf("Creating a cache: %s\n", cacheKey)
			if err := createTar(dir, cacheKey, paths); err != nil {
				log.Fatal(err)
			}
			if err := compressGzip(dir, cacheKey); err != nil {
				log.Fatal(err)
			}
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

	log.Println("Creating a tar file")
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
		walkErr := filepath.Walk(path, func(elempath string, info os.FileInfo, err error) error {
			if err != nil {
				return fmt.Errorf("failed to traverse files: %s", err)
			}

			var link string
			if info.Mode()&os.ModeSymlink == os.ModeSymlink {
				if link, err = os.Readlink(elempath); err != nil {
					return fmt.Errorf("failed to read link: %s", err)
				}
			}

			tarHeader, thErr := tar.FileInfoHeader(info, link)
			if err != nil {
				return fmt.Errorf("failed to create tar Header: %s", thErr)
			}

			tarHeader.Name = filepath.Join(childDir, strings.TrimPrefix(elempath, filepath.Dir(path)))

			if err := tw.WriteHeader(tarHeader); err != nil {
				return fmt.Errorf("failed to write tar header: %s", err)
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			file, fileErr := os.Open(elempath)
			if fileErr != nil {
				return fmt.Errorf("failed to open: %s", fileErr)
			}

			if _, err := io.Copy(tw, file); err != nil {
				return fmt.Errorf("failed to write file: %s", err)
			}

			if err := tw.Flush(); err != nil {
				return fmt.Errorf("failed to flush tar file: %s", err)
			}

			return nil
		})

		if walkErr != nil {
			return walkErr
		}
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

	log.Println("Compressing to a gzip file")
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
	log.Println("Uploading to S3")
	if _, err := s3Client.PutObject(input); err != nil {
		return fmt.Errorf("failed to upload to S3: %s", err)
	}
	log.Println("Uploaded successfully")

	return nil
}
