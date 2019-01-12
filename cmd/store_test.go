package cmd

import (
	"archive/tar"
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func setupFixturesToCache(t *testing.T) {
	clearFixturesToCache(t)

	if err := os.MkdirAll("tmp/foo/bar/baz", 0755); err != nil {
		t.Fatalf("failed to create a directory contains files to cache: %s", err)
	}

	if file, err := os.Create("tmp/foo/hoge.txt"); err != nil {
		t.Fatalf("failed to create a file to cache: %s", err)
	} else {
		if _, err := file.WriteString("This is foo!"); err != nil {
			t.Fatalf("failed to write to a file to cache")
		}
	}

	if err := os.Symlink("../../hoge.txt", "tmp/foo/bar/baz/link"); err != nil {
		t.Fatalf("failed to create a symlink to cache: %s", err)
	}

	if err := os.MkdirAll("tmp/abc/def/ghe", 0755); err != nil {
		t.Fatalf("failed to create a directory contains files to cache: %s", err)
	}
}

func clearFixturesToCache(t *testing.T) {
	if err := os.RemoveAll("tmp"); err != nil {
		t.Fatalf("failed to remove fixtures to cache: %s", err)
	}
}

func assertFixtures(t *testing.T) {
	if stat, err := os.Stat("tmp/foo/bar/baz"); err != nil {
		t.Fatalf("failed to stat a fixture directory: %s", err)
	} else {
		if !stat.IsDir() {
			t.Fatalf("assertion failed: tmp/foo/bar/baz is not a directory")
		}
	}

	if file, err := os.Open("tmp/foo/hoge.txt"); err != nil {
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

	if link, err := os.Readlink("tmp/foo/bar/baz/link"); err != nil {
		t.Fatalf("failed to stat a fixture symlink: %s", err)
	} else {
		if link != "../../hoge.txt" {
			t.Fatalf("the target of a fixture link is wrong: %s", link)
		}
	}

	if stat, err := os.Stat("tmp/abc/def/ghe"); err != nil {
		t.Fatalf("failed to stat a fixture directory: %s", err)
	} else {
		if !stat.IsDir() {
			t.Fatalf("assertion failed: tmp/abc/def/ghe is not a directory")
		}
	}
}

func TestAssertFixtures(t *testing.T) {
	setupFixturesToCache(t)
	assertFixtures(t)
}

type TarHeaderAndContent struct {
	Header  *tar.Header
	Content string
}

func TestCreateTar(t *testing.T) {
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

	if file, err := os.Open(filepath.Join(dir, "test.tar")); err != nil {
		t.Fatalf("failed to open the created tar: %s", err)
	} else {
		tr := tar.NewReader(file)
		hdrs := make(map[string]*TarHeaderAndContent)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("failed to read the tar file: %s", err)
			}

			buf := new(bytes.Buffer)

			if hdr.Typeflag&tar.TypeDir != tar.TypeDir && hdr.Typeflag&tar.TypeSymlink != tar.TypeSymlink {
				_, rErr := io.Copy(buf, tr)
				if rErr != nil {
					t.Fatalf("failed to read a file from the tar file: %s", rErr)
				}
			}

			hdrs[hdr.Name] = &TarHeaderAndContent{
				Header:  hdr,
				Content: buf.String(),
			}
		}

		n := len(hdrs)
		if n != 8 {
			t.Fatalf("the number of the entries is wrong: %d", n)
		}

		if hdrs["metadata.json"].Content != `{"paths":["tmp/foo","tmp/abc/def"]}` {
			t.Fatalf("the content of metadata.json is wrong: %s", hdrs["metadata.json"].Content)
		}
		if hdrs["0000/foo/hoge.txt"].Content != "This is foo!" {
			t.Fatalf("the content of 0000/foo/hoge.txt is wrong: %s", hdrs["0000/tmp/foo/hoge.txt"].Content)
		}
		if hdrs["0000/foo/bar/baz/link"].Header.Linkname != "../../hoge.txt" {
			t.Fatalf("the target of the link 0000/tmp/foo/bar/baz/link is wrong: %s", hdrs["0000/tmp/foo/bar/baz/link"].Header.Linkname)
		}
		if hdrs["0001/def/ghe"].Header.Typeflag&tar.TypeDir != tar.TypeDir {
			t.Fatalf("the directory 0001/abc/def/ghe is not a directory")
		}
	}
}
