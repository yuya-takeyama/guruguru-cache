package template

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/shirou/gopsutil/cpu"
)

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

// ExecuteTemplate executes template of a cache key
func ExecuteTemplate(s string) (string, error) {
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
