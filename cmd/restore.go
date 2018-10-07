package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/spf13/cobra"
	"github.com/yuya-takeyama/guruguru-cache/template"
)

func init() {
	restoreCmd.Flags().StringVarP(&s3Bucket, "s3-bucket", "", "", "S3 bucket to upload")
	restoreCmd.MarkFlagRequired("s3-bucket")

	rootCmd.AddCommand(restoreCmd)

	sess := session.Must(session.NewSession())
	s3Client = s3.New(sess)
}

var restoreCmd = &cobra.Command{
	Use:   "restore [flags] [cache keys...]",
	Short: "Restore cache files with keys",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dir, err := ioutil.TempDir("", "guruguru-cache-")
		if err != nil {
			log.Fatalf("failed to create temporal directory: %s", err)
		}

		defer os.RemoveAll(dir)

		var item *s3.GetObjectOutput
		for _, key := range args {
			cacheKey, err := template.ExecuteTemplate(key)
			if err != nil {
				log.Fatal(err)
			}

			log.Printf("checking cache for: %s", cacheKey)

			item, err = getExactlyMatchedItem(cacheKey)
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					if aerr.Code() != s3.ErrCodeNoSuchKey {
						log.Printf("error occurred when fetching exactly matched item: %s", err)
					}
				}
			}
			if item != nil && item.Body != nil {
				log.Printf("exact matched cache is found: %s", cacheKey)
				break
			}

			var itemKey string
			item, itemKey, err = getPartiallyMatchedItem(cacheKey)
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					if aerr.Code() != s3.ErrCodeNoSuchKey {
						log.Printf("error occurred when fetching partially matched item: %s", err)
					}
				}
			}
			if item != nil && item.Body != nil {
				log.Printf("partially matched cache is found for %s: %s", cacheKey, itemKey)
				break
			}
		}

		if item == nil {
			log.Println("no cache is found")
			return
		}

		extractCache(dir, item)
		moveToOriginalPaths(dir)
	},
}

func getExactlyMatchedItem(cacheKey string) (*s3.GetObjectOutput, error) {
	key := cacheKey + ".tar.gz"
	input := &s3.GetObjectInput{
		Bucket: &s3Bucket,
		Key:    &key,
	}
	return s3Client.GetObject(input)
}

var maxKeys = int64(1000)

func getPartiallyMatchedItem(cacheKey string) (*s3.GetObjectOutput, string, error) {
	ctx := context.Background()
	input := &s3.ListObjectsV2Input{
		Bucket:  &s3Bucket,
		Prefix:  &cacheKey,
		MaxKeys: &maxKeys,
	}

	var result *s3.Object
	latest := new(time.Time)
	err := s3Client.ListObjectsV2PagesWithContext(ctx, input, func(output *s3.ListObjectsV2Output, haxNextPage bool) bool {
		for _, object := range output.Contents {
			if latest.Before(*object.LastModified) {
				result = object
				latest = object.LastModified
			}
		}

		return true
	})
	if err != nil {
		return nil, "", err
	}

	if result != nil {
		input := &s3.GetObjectInput{
			Bucket: &s3Bucket,
			Key:    result.Key,
		}
		output, err := s3Client.GetObject(input)
		if err != nil {
			return nil, "", err
		}

		return output, *result.Key, nil
	}

	return nil, "", nil
}

func extractCache(dir string, item *s3.GetObjectOutput) {
	defer item.Body.Close()

	file, err := os.Create(filepath.Join(dir, "cache.tar.gz"))
	if err != nil {
		log.Fatalf("failed to create cache file: %s", err)
	}

	defer file.Close()

	if _, err := io.Copy(file, item.Body); err != nil {
		log.Fatalf("failed to save cache file: %s", err)
	}

	fmt.Println(dir)

	file.Seek(0, 0)

	gzr, err := gzip.NewReader(file)
	if err != nil {
		log.Fatalf("failed to open gzip file: %s", err)
	}

	tr := tar.NewReader(gzr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("failed to extract tar file: %s", err)
		}

		target := filepath.Join(dir, hdr.Name)
		fileDir := filepath.Dir(target)
		if err := os.MkdirAll(fileDir, 0755); err != nil {
			log.Fatalf("failed to create a directory: %s", err)
		}

		f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(hdr.Mode))
		if err != nil {
			log.Fatalf("failed to create a file: %s", err)
		}

		defer f.Close()

		if _, err := io.Copy(f, tr); err != nil {
			log.Fatalf("failed to write to a file: %s", err)
		}
	}
}

func moveToOriginalPaths(dir string) {
	metadataFile, err := os.Open(filepath.Join(dir, "metadata.json"))
	if err != nil {
		if err != nil {
			log.Fatalf("failed to open metadata file: %s", err)
		}
	}

	var meta metadata

	jd := json.NewDecoder(metadataFile)
	if err := jd.Decode(&meta); err != nil {
		log.Fatalf("failed to decode metadata file: %s", err)
	}

	for i, path := range meta.Paths {
		if err := os.RemoveAll(path); err != nil {
			log.Fatalf("failed to remove current path: %s: %s", path, err)
		}

		from := filepath.Join(dir, fmt.Sprintf("%04d", i), filepath.Base(path))
		os.Rename(from, path)
	}

	log.Println("finished")
}
