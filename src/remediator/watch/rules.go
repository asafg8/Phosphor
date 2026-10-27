package watch

import "math"

// Sufficient is the baseline rule: requests ≥ 200 && rpm ≥ 1.
func Sufficient(requests int, rpm float64) bool { return requests >= 200 && rpm >= 1 }

// Threshold5xx = max(2×base, base+1%).
func Threshold5xx(baseRate float64) float64 { return math.Max(2*baseRate, baseRate+0.01) }

// Bucket is one minute of traffic.
type Bucket struct{ Req, S5xx int }

func (b Bucket) Rate() float64 {
	if b.Req == 0 {
		return 0
	}
	return float64(b.S5xx) / float64(b.Req)
}

// Over5xx reports whether a bucket is strictly above the threshold (exactly at threshold is not).
func Over5xx(b Bucket, baseRate float64) bool {
	return b.Req > 0 && b.Rate() > Threshold5xx(baseRate)+1e-12
}

// FiveXXTrigger fires on the 3rd consecutive over-threshold minute bucket.
func FiveXXTrigger(buckets []Bucket, baseRate float64) bool {
	run := 0
	for _, b := range buckets {
		if Over5xx(b, baseRate) {
			run++
			if run >= 3 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}
