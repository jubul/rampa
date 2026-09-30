package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"time"
)

// chartModTime es la fecha fija de las entradas del tar: el chart embebido no
// tiene fechas propias y así el archivo es reproducible entre arranques.
var chartModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// PackageChart empaqueta un chart embebido como tar.gz con el directorio raíz
// root (el formato que espera helm install <url> y loader.LoadArchive), sin
// dependencias más allá de archive/tar y compress/gzip. Las entradas van en
// orden estable.
func PackageChart(fsys fs.FS, root string) ([]byte, error) {
	if _, err := fs.Stat(fsys, root); err != nil {
		return nil, fmt.Errorf("el chart %s no está embebido: %w", root, err)
	}
	var files []string
	if err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("el chart %s está vacío", root)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dirs := map[string]bool{}
	for _, f := range files {
		// Directorios intermedios antes de sus archivos.
		for dir := path.Dir(f); dir != "." && dir != "/" && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	for _, d := range dirList {
		if err := tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: chartModTime}); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		content, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("leyendo %s: %w", f, err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: f, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content)), ModTime: chartModTime}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
