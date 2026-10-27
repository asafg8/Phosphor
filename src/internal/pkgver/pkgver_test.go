package pkgver

import "testing"

func TestCompareDeb(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"7.4.33-1+deb11u12", "7.4.33-1+deb11u5", 1},
		{"1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1", "1:7.4.33-31", 1},
		{"7.4.33-30+ubuntu22.04.1+deb.sury.org+1", "1:7.4.33-31", -1},
		{"1:7.4.33-30", "1:7.4.33-31", -1},
		{"8.2.35-1~deb12u1", "8.2.35-1", -1},
		{"8.2.35-1~deb12u1", "8.2.35-1~deb12u1", 0},
		{"7.4.3-4ubuntu2.29", "7.4.3-4ubuntu2.29+esm5", -1},
		{"5.6.40-104+ubuntu22.04.1+deb.sury.org+1", "5.6.40-104", 1},
	}
	for _, c := range cases {
		if got := CompareDeb(c.a, c.b); got != c.want {
			t.Errorf("deb %s vs %s = %d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareRPM(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"7.4.33-6.module_el8.10.0+3912+bdd2b75b", "7.4.33-8", -1},
		{"7.4.33-8.module_el8.10.0+4000+aaaa", "7.4.33-8", 1},
		{"8.2.34-1.amzn2023.0.1", "8.2.35-1.amzn2023.0.1", -1},
		{"8.2.35-1.amzn2023.0.1", "8.2.35-1.amzn2023.0.1", 0},
		{"8.1.32-1.module_el9.8.0+326+528679fc", "8.1.32-1.module_el9.8.0+326+528679fc.alma.1", -1},
	}
	for _, c := range cases {
		if got := CompareRPM(c.a, c.b); got != c.want {
			t.Errorf("rpm %s vs %s = %d want %d", c.a, c.b, got, c.want)
		}
	}
}
