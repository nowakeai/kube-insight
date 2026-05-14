package benchmark

import "testing"

func TestSummarizeLatency(t *testing.T) {
	summary := summarizeLatency([]float64{5, 1, 3, 2, 4})
	if summary.Count != 5 {
		t.Fatalf("count = %d, want 5", summary.Count)
	}
	if summary.P50MS != 3 {
		t.Fatalf("p50 = %f, want 3", summary.P50MS)
	}
	if summary.P95MS != 5 {
		t.Fatalf("p95 = %f, want 5", summary.P95MS)
	}
	if summary.MinMS != 1 || summary.MaxMS != 5 {
		t.Fatalf("min/max = %f/%f", summary.MinMS, summary.MaxMS)
	}
}
