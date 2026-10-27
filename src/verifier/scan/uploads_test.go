package scan

import (
	"os"
	"path/filepath"
	"testing"

	c "github.com/asafg8/Phosphor/src/contract"
)

// $_FILES → used; none → unused; dynamic superglobal access → unknown.
func TestU12_Uploads(t *testing.T) {
	d := t.TempDir()
	write := func(n, s string) string {
		p := filepath.Join(d, n)
		_ = os.WriteFile(p, []byte(s), 0o644)
		return p
	}
	cases := map[string]string{
		"used":    "<?php\nif (!empty($_FILES['f'])) {}\n",
		"unused":  "<?php\necho 1;\n",
		"unknown": "<?php\n$x = ${'_FI'.'LES'};\n",
	}
	for want, src := range cases {
		f := &Feature{}
		ClassifyFile(write(want+".php", src), f, nil)
		if got := f.Verdict(); string(got) != want {
			t.Errorf("%s: got %s", want, got)
		}
	}
	f := &Feature{}
	ClassifyFile(write("g.php", "<?php\n$v = $GLOBALS[$name];\n"), f, nil)
	if f.Verdict() != "unknown" {
		t.Errorf("GLOBALS: %s", f.Verdict())
	}
	f = &Feature{}
	ClassifyFile(write("u.php", "<?php\n$a = $_FILES;\n"), f, nil)
	if len(f.Evidence()) != 1 || f.Evidence()[0].Line != 2 {
		t.Errorf("evidence %+v", f.Evidence())
	}
}

// Code that could not be read is not evidence of non-use.
func TestUploadsIncompleteIsUnknown(t *testing.T) {
	f := &Feature{}
	ClassifyFile(filepath.Join(t.TempDir(), "missing.php"), f, nil)
	if f.Verdict() != "unknown" {
		t.Errorf("unreadable file: %s", f.Verdict())
	}
	f = &Feature{Incomplete: true, Used: []c.FileLine{{File: "a.php", Line: 1}}}
	if f.Verdict() != "used" {
		t.Errorf("used + incomplete: %s", f.Verdict())
	}
}

// Session upload progress only; Drupal 7's APC/PECL uploadprogress code is not the session feature.
func TestUploadProgress(t *testing.T) {
	d := t.TempDir()
	cases := map[string]string{
		"used":   "<?php\n$key = ini_get('session.upload_progress.prefix') . $_GET['id'];\n",
		"used2":  "<?php\n$s = $_SESSION['upload_progress_' . $id];\n",
		"unused": "<?php\n$upload_progress_key = mt_rand();\n$e['APC_UPLOAD_PROGRESS'] = $upload_progress_key;\nuploadprogress_get_info($upload_progress_key);\n",
	}
	for name, src := range cases {
		p := filepath.Join(d, name+".module")
		_ = os.WriteFile(p, []byte(src), 0o644)
		up, prog := &Feature{}, &Feature{}
		ClassifyFile(p, up, prog)
		want := c.Usage("used")
		if name == "unused" {
			want = "unused"
		}
		if prog.Verdict() != want {
			t.Errorf("%s: %s", name, prog.Verdict())
		}
	}
	p := filepath.Join(d, "f.php")
	_ = os.WriteFile(p, []byte("<?php\nmove_uploaded_file($a, $b);\n"), 0o644)
	up := &Feature{}
	ClassifyFile(p, up, nil)
	u := up.Use("file_uploads", 10)
	if u.Verdict != "used" || len(u.Evidence) != 1 || u.Evidence[0].Kind != "move_uploaded_file" || u.Evidence[0].Line != 2 {
		t.Errorf("use %+v", u)
	}
}
