package cmd

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCache(t *testing.T) {
	setupFixturesToCache(t)

	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		log.Fatalf("failed to create temporal directory: %s", err)
	}

	defer os.RemoveAll(dir)

	paths := []string{"tmp/foo", "tmp/abc/def"}
	if err := createTar(dir, "test", paths); err != nil {
		t.Fatalf("failed to create a tar: %s", err)
	}
	if err := compressGzip(dir, "test"); err != nil {
		t.Fatalf("failed to compress to gzip file: %s", err)
	} else {
		if file, err := os.Open(filepath.Join(dir, "test.tar.gz")); err != nil {
			t.Fatalf("failed to open the gzip file: %s", err)
		} else {
			extractCache(dir, file)

			if stat, err := os.Stat(filepath.Join(dir, "0000/foo/bar/baz")); err != nil {
				t.Fatalf("failed to stat a fixture directory: %s", err)
			} else {
				if !stat.IsDir() {
					t.Fatalf("assertion failed: 0000/foo/bar/baz is not a directory")
				}
			}

			if file, err := os.Open(filepath.Join(dir, "0000/foo/hoge.txt")); err != nil {
				t.Fatalf("failed to open a fixture file: %s", err)
			} else {
				defer file.Close()

				if content, err := ioutil.ReadAll(file); err != nil {
					t.Fatalf("failed to read a fixture file: %s", err)
				} else {
					str := string(content)
					if str != "This is foo!" {
						t.Fatalf("the content of a fixture file is wrong: %s", str)
					}
				}
			}

			if link, err := os.Readlink(filepath.Join(dir, "0000/foo/bar/baz/link")); err != nil {
				t.Fatalf("failed to stat a fixture symlink: %s", err)
			} else {
				if link != "../../hoge.txt" {
					t.Fatalf("the target of a fixture link is wrong: %s", link)
				}
			}

			if stat, err := os.Stat(filepath.Join(dir, "0001/def/ghe")); err != nil {
				t.Fatalf("failed to stat a fixture directory: %s", err)
			} else {
				if !stat.IsDir() {
					t.Fatalf("assertion failed: tmp/def/ghe is not a directory")
				}
			}
		}
	}
}

func TestMoveToOriginalPathWith(t *testing.T) {
	setupFixturesToCache(t)

	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		log.Fatalf("failed to create temporal directory: %s", err)
	}

	defer os.RemoveAll(dir)

	paths := []string{"tmp/foo", "tmp/abc/def"}
	if err := createTar(dir, "test", paths); err != nil {
		t.Fatalf("failed to create a tar: %s", err)
	}
	if err := compressGzip(dir, "test"); err != nil {
		t.Fatalf("failed to compress to gzip file: %s", err)
	} else {
		clearFixturesToCache(t)

		if file, err := os.Open(filepath.Join(dir, "test.tar.gz")); err != nil {
			t.Fatalf("failed to open the gzip file: %s", err)
		} else {
			extractCache(dir, file)
			moveToOriginalPaths(dir)
			assertFixtures(t)
		}
	}
}
