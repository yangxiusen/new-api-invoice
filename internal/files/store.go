package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"invoice/internal/fault"
)

const MaxSize = 10 * 1024 * 1024

type Prepared struct {
	Name, Path, MIME, Digest string
	Size                     int64
}
type Store struct {
	root     *os.Root
	absolute string
}

func New(dir string) (*Store, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, e
	}
	resolved, e := filepath.EvalSymlinks(abs)
	if e != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(abs)) {
		return nil, errors.New("file root must not contain symlinks")
	}
	root, e := os.OpenRoot(abs)
	if e != nil {
		return nil, e
	}
	return &Store{root: root, absolute: abs}, nil
}
func (s *Store) Close() error { return s.root.Close() }
func safeName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 {
		return ""
	}
	return strings.TrimSpace(name)
}
func (s *Store) Safe(rel string) error {
	if !fs.ValidPath(rel) || rel == "." || strings.ContainsAny(rel, "\\:") {
		return errors.New("unsafe file path")
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		info, e := s.root.Lstat(p)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink rejected")
		}
	}
	return nil
}
func (s *Store) Open(rel string) (*os.File, error) {
	if e := s.Safe(rel); e != nil {
		return nil, e
	}
	f, e := s.root.Open(rel)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}
func (s *Store) Prepare(name, declared string, r io.Reader) (Prepared, error) {
	var p Prepared
	p.Name = safeName(name)
	if p.Name == "" {
		return p, fault.New(400, "INVALID_ARGUMENT", "文件名不正确")
	}
	ext := strings.ToLower(path.Ext(p.Name))
	expected := map[string]string{".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg"}[ext]
	if expected == "" {
		return p, fault.New(415, "UNSUPPORTED_MEDIA_TYPE", "仅支持 PDF、JPG 和 PNG")
	}
	raw, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil {
		return p, e
	}
	if len(raw) > MaxSize {
		return p, fault.New(413, "FILE_TOO_LARGE", "文件不能超过 10MB")
	}
	if len(raw) == 0 || http.DetectContentType(raw) != expected || declared != expected {
		return p, fault.New(400, "INVALID_ARGUMENT", "文件内容、类型和扩展名不一致")
	}
	if expected != "application/pdf" {
		cfg, _, e := image.DecodeConfig(bytes.NewReader(raw))
		if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 20000 || cfg.Height > 20000 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
			return p, fault.New(400, "INVALID_ARGUMENT", "图片损坏或尺寸过大")
		}
		if _, _, e = image.Decode(bytes.NewReader(raw)); e != nil {
			return p, fault.New(400, "INVALID_ARGUMENT", "图片无法解码")
		}
	}
	year := time.Now().UTC().Format("2006")
	if e = s.root.Mkdir(year, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return p, e
	}
	if e = s.Safe(year); e != nil {
		return p, e
	}
	dir := year + "/" + uuid.NewString()
	if e = s.root.Mkdir(dir, 0700); e != nil {
		return p, e
	}
	temp := dir + "/pending"
	f, e := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return p, e
	}
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		_ = s.root.Remove(temp)
		_ = s.root.Remove(dir)
		return p, e
	}
	p.Path = dir + "/object" + ext
	if e = s.root.Rename(temp, p.Path); e != nil {
		return p, e
	}
	// File contents and directory entries must both survive a Linux container crash.
	if runtime.GOOS != "windows" {
		for _, entry := range []string{dir, year, "."} {
			d, err := s.root.Open(entry)
			if err != nil {
				return p, err
			}
			err = d.Sync()
			closeErr := d.Close()
			if err != nil {
				return p, err
			}
			if closeErr != nil {
				return p, closeErr
			}
		}
	}
	digest := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(digest[:])
	p.Size = int64(len(raw))
	p.MIME = expected
	return p, nil
}
func (s *Store) Remove(rel string) error {
	if e := s.Safe(rel); e != nil {
		return e
	}
	if e := s.root.Remove(rel); e != nil {
		return e
	}
	return s.root.Remove(path.Dir(rel))
}
func (s *Store) Paths() ([]string, error) {
	result := []string{}
	e := fs.WalkDir(s.root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in file root")
		}
		if !d.IsDir() {
			result = append(result, p)
		}
		return nil
	})
	return result, e
}
func (s *Store) Writable() error {
	f, e := s.root.OpenFile(".health-"+uuid.NewString(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	name := filepath.Base(f.Name())
	e = f.Close()
	remove := s.root.Remove(name)
	if e != nil {
		return e
	}
	return remove
}
