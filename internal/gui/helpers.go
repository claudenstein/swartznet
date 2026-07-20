package gui

import "fmt"

// humanBytes renders binary sizes: "512 B", "1.5 KiB", "2.0 GiB".
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(1024), 0
	for u := n / 1024; u >= 1024; u /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// short16 returns the first 16 chars of s (or all of it if shorter) — used to
// abbreviate infohashes/pubkeys in dense list rows.
func short16(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

// short8 returns the first 8 chars of s.
func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
