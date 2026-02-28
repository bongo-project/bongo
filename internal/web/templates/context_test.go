package templates

import (
	"reflect"
	"testing"
)

func TestPageWindow(t *testing.T) {
	tests := []struct {
		name       string
		page       int
		totalPages int
		want       []int
	}{
		{"small", 3, 5, []int{1, 2, 3, 4, 5}},
		{"exactly 9", 5, 9, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"first page large", 1, 46, []int{1, 2, 3, 0, 46}},
		{"last page large", 46, 46, []int{1, 0, 44, 45, 46}},
		{"middle large", 20, 46, []int{1, 0, 18, 19, 20, 21, 22, 0, 46}},
		{"near start", 3, 46, []int{1, 2, 3, 4, 5, 0, 46}},
		{"near end", 44, 46, []int{1, 0, 42, 43, 44, 45, 46}},
		{"single page", 1, 1, []int{1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pageWindow(tc.page, tc.totalPages)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pageWindow(%d, %d) = %v, want %v", tc.page, tc.totalPages, got, tc.want)
			}
		})
	}
}
