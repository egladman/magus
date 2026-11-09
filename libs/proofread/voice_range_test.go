package proofread

import "testing"

func TestBuildVoiceKeepsARangeOfEachFeatureItsWindowsMeasure(t *testing.T) {
	r, ok := authorVoice(t).Kinds[KindReviewReply].Ranges["opener_class_entropy"]

	if !ok || r.Windows < minRangeWindows || r.P5 > r.P95 {
		t.Errorf("opener_class_entropy range = %+v, %v, want one from %d windows or more", r, ok, minRangeWindows)
	}
}

func TestBuildVoiceKeepsNoRangeUnderTheWordFloor(t *testing.T) {
	v, err := BuildVoice(map[Kind][]string{KindIssue: {"The cache misses on a cold start. It warms after one run."}})
	if err != nil {
		t.Fatal(err)
	}

	if got := v.Kinds[KindIssue].Ranges; got != nil {
		t.Errorf("Ranges = %v, want none under %d words", got, MinVoiceWords)
	}
}

func TestReadRangeFeatureReadsNoUnknownFeature(t *testing.T) {
	if _, ok := readRangeFeature(Shape{Words: 10}, "typos"); ok {
		t.Error("readRangeFeature read a feature no range is kept of")
	}
}
