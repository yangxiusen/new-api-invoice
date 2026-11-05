package files

import (
	"bytes"
	"os"
	"testing"
)

func TestPrivateFiles(t *testing.T) {
	root := t.TempDir()
	s, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	raw := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	p, e := s.Prepare("发票.pdf", "application/pdf", bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Open(p.Path)
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	if _, e = s.Open("../outside.pdf"); e == nil {
		t.Fatal("accepted traversal")
	}
	for _, c := range []struct {
		name, mime string
		raw        []byte
	}{{"a.png", "image/png", raw}, {"a.exe", "application/pdf", raw}, {"a.pdf", "image/jpeg", raw}, {"a.pdf", "application/pdf", nil}} {
		if _, e = s.Prepare(c.name, c.mime, bytes.NewReader(c.raw)); e == nil {
			t.Errorf("accepted %s/%s", c.name, c.mime)
		}
	}
	if _, e = s.Prepare("big.pdf", "application/pdf", bytes.NewReader(make([]byte, 10*1024*1024+1))); e == nil {
		t.Fatal("accepted large file")
	}
	outside := t.TempDir()
	if e = os.WriteFile(outside+"/a.pdf", raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, root+"/link"); e == nil {
		if _, e = s.Open("link/a.pdf"); e == nil {
			t.Fatal("accepted symlink")
		}
	}
}
