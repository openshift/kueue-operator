package bindata

import (
	"embed"
	"fmt"
)

//go:embed assets
var f embed.FS

// Asset reads and returns the content of the named file.
func Asset(name string) ([]byte, error) {
	return f.ReadFile(name)
}

// MustAsset reads and returns the content of the named file or panics
// if something went wrong.
func MustAsset(name string) []byte {
	data, err := f.ReadFile(name)
	if err != nil {
		panic(err)
	}

	return data
}

// AssetDir returns a list of files in a specific directory.
func AssetDir(dir string) ([]string, error) {
	var files []string
	entries, err := f.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}
