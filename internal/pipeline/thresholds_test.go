package pipeline

import (
	"math"
	"sync"
	"testing"
)

func TestSpamThresholdsValidate(t *testing.T) {
	nan := float32(math.NaN())
	tests := []struct {
		name    string
		t       SpamThresholds
		wantErr bool
	}{
		{"defaults", SpamThresholds{0.5, 0.9}, false},
		{"equal accepted", SpamThresholds{0.5, 0.5}, false},
		{"one accepted", SpamThresholds{1, 1}, false},
		{"tag zero", SpamThresholds{0, 0.9}, true},
		{"tag above one", SpamThresholds{1.1, 1.1}, true},
		{"high below tag", SpamThresholds{0.6, 0.5}, true},
		{"high above one", SpamThresholds{0.5, 1.1}, true},
		{"tag NaN", SpamThresholds{nan, 0.9}, true},
		{"high NaN", SpamThresholds{0.5, nan}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.t.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%+v) err = %v, wantErr %v", tc.t, err, tc.wantErr)
			}
		})
	}
}

func TestSpamThresholdHolderStoreRejectsAndKeepsOld(t *testing.T) {
	want := SpamThresholds{0.4, 0.8}
	h, err := NewSpamThresholdHolder(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store(SpamThresholds{0.9, 0.5}); err == nil {
		t.Fatal("Store accepted high < tag")
	}
	if got := h.Thresholds(); got != want {
		t.Fatalf("holder changed after rejected Store: got %+v want %+v", got, want)
	}
	if _, err := NewSpamThresholdHolder(SpamThresholds{}); err == nil {
		t.Fatal("NewSpamThresholdHolder accepted zero pair")
	}
}

func TestSpamThresholdHolderConcurrentConsistentPair(t *testing.T) {
	a := SpamThresholds{0.3, 0.6}
	b := SpamThresholds{0.7, 0.95}
	h, err := NewSpamThresholdHolder(a)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				v := a
				if i%2 == 1 {
					v = b
				}
				if err := h.Store(v); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	var rwg sync.WaitGroup
	for range 4 {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for range 200000 {
				if got := h.Thresholds(); got != a && got != b {
					t.Errorf("torn pair: %+v", got)
					return
				}
			}
		}()
	}
	rwg.Wait()
	close(stop)
	wg.Wait()
}
