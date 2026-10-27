package watch

import "testing"

// 5xx trigger = max(2×base, base+1%) for 3 consecutive buckets; 2 buckets no; exactly threshold no.
func TestU05_FiveXX(t *testing.T) {
	base := 0.0
	over := Bucket{Req: 100, S5xx: 20}
	ok := Bucket{Req: 100, S5xx: 0}
	if !FiveXXTrigger([]Bucket{over, over, over}, base) {
		t.Error("3 over must trigger")
	}
	if FiveXXTrigger([]Bucket{over, over, ok, over, over}, base) {
		t.Error("2+2 must not trigger")
	}
	at := Bucket{Req: 100, S5xx: 1} // exactly 1% with base 0
	if FiveXXTrigger([]Bucket{at, at, at}, base) {
		t.Error("exactly at threshold must not trigger")
	}
	if Threshold5xx(0.1) != 0.2 || Threshold5xx(0.002) != 0.012 {
		t.Error("threshold math")
	}
	below := Bucket{Req: 1000, S5xx: 5} // 0.5% < 1%
	if FiveXXTrigger([]Bucket{below, below, below, below}, 0) {
		t.Error("below threshold triggered")
	}
}

func TestU05_Sufficient(t *testing.T) {
	if !Sufficient(200, 1) || Sufficient(199, 5) || Sufficient(500, 0.5) {
		t.Error("sufficient rule")
	}
}

func TestU05_Immediate(t *testing.T) {
	if !Classify(`[24-Oct-2026 12:00:00] WARNING: [pool www] child 123 exited on signal 11 (SIGSEGV - core dumped) after 1.2 seconds from start`).Segv {
		t.Error("fpm segv")
	}
	if !Classify(`[Sat Oct 24 12:00:00.1 2026] [core:notice] [pid 50] AH00052: child pid 77 exit signal Segmentation fault (11)`).Segv {
		t.Error("apache segv")
	}
	if !Classify(`PHP Fatal error:  Uncaught Error: Call to undefined function x() in /var/www/a.php:3`).Fatal {
		t.Error("fatal")
	}
}

// Signature normalisation masks digits/hex/quoted strings; same fatal on another line → same signature.
func TestU06_Signature(t *testing.T) {
	a := Classify(`2026/10/24 12:00:00 [error] 12#12: *34 FastCGI sent in stderr: "PHP message: PHP Fatal error:  Uncaught Error: Call to undefined function phosphor_x() in /var/www/wordpress/index.php:3\nStack trace:\n#0 {main}" while reading response header`)
	b := Classify(`2026/10/24 12:05:00 [error] 12#12: *99 FastCGI sent in stderr: "PHP message: PHP Fatal error:  Uncaught Error: Call to undefined function phosphor_x() in /var/www/wordpress/index.php:17\nStack trace:\n#0 {main}" while reading response header`)
	if a.Sig == "" || a.Sig != b.Sig || !a.Fatal {
		t.Errorf("sig %q vs %q", a.Sig, b.Sig)
	}
	c := Classify(`[Sat Oct 24 12:00:00 2026] [php7:error] [pid 52] [client 172.19.0.3:4455] PHP Fatal error:  Uncaught Error: Call to undefined function imagecreatetruecolor() in /usr/local/lib/phosphor-lab-kit/kit.php:32`)
	if c.Sig != "PHP Fatal error|Uncaught Error: Call to undefined function imagecreatetruecolor()|/usr/local/lib/phosphor-lab-kit/kit.php" {
		t.Errorf("apache sig %q", c.Sig)
	}
	d := Classify(`PHP Warning:  Division by zero in /a.php on line 4`)
	e := Classify(`PHP Warning:  Division by zero in /a.php on line 9`)
	if d.Sig != e.Sig || d.Fatal {
		t.Errorf("5.6-style %q %q", d.Sig, e.Sig)
	}
	if Mask(`id 123 at 0x7ffe 'abc' "x"`) != `id N at 0xH 'S' 'S'` {
		t.Errorf("mask %q", Mask(`id 123 at 0x7ffe 'abc' "x"`))
	}
}
