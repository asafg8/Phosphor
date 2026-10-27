package watch

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	c "github.com/asafg8/Phosphor/src/contract"
)

// Pos is where a tail continues (contract.WatchPos).
type Pos = c.WatchPos

func inode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// Current returns the end position of a file now.
func Current(path string) Pos {
	fi, err := os.Stat(path)
	if err != nil {
		return Pos{}
	}
	return Pos{Inode: inode(fi), Offset: fi.Size()}
}

// ReadNew returns complete lines appended since pos and the new position. If the file was rotated (inode
// changed or truncated), the rest of the old file is read from its rotated name (path.1) first.
func ReadNew(path string, pos Pos) ([]string, Pos) {
	var lines []string
	fi, err := os.Stat(path)
	if err != nil {
		return nil, pos
	}
	ino := inode(fi)
	if pos.Inode != 0 && ino != pos.Inode {
		for _, cand := range []string{path + ".1", path + "-" + time.Now().Format("20060102")} {
			if ofi, err := os.Stat(cand); err == nil && inode(ofi) == pos.Inode {
				l, _ := readFrom(cand, pos.Offset)
				lines = append(lines, l...)
				break
			}
		}
		pos = Pos{Inode: ino, Offset: 0}
	} else if fi.Size() < pos.Offset {
		pos.Offset = 0 // truncated in place (copytruncate)
	}
	if pos.Inode == 0 {
		pos.Inode = ino
	}
	l, off := readFrom(path, pos.Offset)
	lines = append(lines, l...)
	pos.Offset = off
	return lines, pos
}

func readFrom(path string, off int64) ([]string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, off
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off
	}
	r := bufio.NewReaderSize(f, 256<<10)
	var lines []string
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			break // partial last line stays for the next read
		}
		off += int64(len(l))
		lines = append(lines, l[:len(l)-1])
	}
	return lines, off
}

// ---- access log parsing (combined/common) ----

var (
	accessRe = regexp.MustCompile(`^\S+ \S+ \S+ \[([^\]]+)\] "(?:(\S+) (\S+)[^"]*|[^"]*)" (\d{3}) `)
	rlRe     = regexp.MustCompile(` rl=(\d+)`)
	ctRe     = regexp.MustCompile(` ct="([^"]*)"`)
	probeUA  = regexp.MustCompile(`phosphor-watch-probe`)
)

// Access is one parsed access-log line (combined/common; nginx `rl=$request_length ct="…"` suffix optional).
type Access struct {
	T         time.Time
	Status    int
	Probe     bool
	Method    string
	Path      string
	ReqLen    int  // request_length when logged, else 0
	Multipart bool // content type multipart/form-data when logged
}

func ParseAccess(line string) (Access, bool) {
	m := accessRe.FindStringSubmatch(line)
	if m == nil {
		return Access{}, false
	}
	t, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[1])
	if err != nil {
		return Access{}, false
	}
	st, _ := strconv.Atoi(m[4])
	a := Access{T: t, Status: st, Probe: probeUA.MatchString(line), Method: m[2], Path: m[3]}
	if r := rlRe.FindStringSubmatch(line); r != nil {
		a.ReqLen, _ = strconv.Atoi(r[1])
	}
	if ct := ctRe.FindStringSubmatch(line); ct != nil {
		a.Multipart = strings.HasPrefix(ct[1], "multipart/")
	}
	return a, true
}
