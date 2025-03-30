package paginator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestFetchPaginated(t *testing.T) {
	t.Parallel()

	tcs := []struct {
		name    string
		perPage int
		pages   [][]int
		want    []int
	}{{
		name:    "no results",
		perPage: 5,
		pages:   [][]int{{}},
		want:    []int{},
	}, {
		name:    "single page",
		perPage: 5,
		pages:   [][]int{{1, 2, 3}},
		want:    []int{1, 2, 3},
	}, {
		name:    "single full page",
		perPage: 5,
		pages:   [][]int{{1, 2, 3, 4, 5}, {}},
		want:    []int{1, 2, 3, 4, 5},
	}, {
		name:    "two pages",
		perPage: 5,
		pages:   [][]int{{1, 2, 3, 4, 5}, {6}},
		want:    []int{1, 2, 3, 4, 5, 6},
	}, {
		name:    "two full pages",
		perPage: 5,
		pages:   [][]int{{1, 2, 3, 4, 5}, {6, 7, 8, 9, 10}, {}},
		want:    []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := FetchPaginated(tc.perPage, func(page int, perPage int) ([]int, error) {
				if perPage != tc.perPage {
					return nil, fmt.Errorf("invalid perPage value")
				}
				return tc.pages[page], nil
			})
			if err != nil {
				t.Fatalf("FetchPaginated: %v", err)
			}

			if diff := cmp.Diff(got, tc.want, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("FetchPaginated diff: -got +want\n%s", diff)
			}
		})
	}
}

func TestFetchPaginatedErr(t *testing.T) {
	t.Parallel()

	var sentinelError = errors.New("sentinel error")
	_, err := FetchPaginated(50, func(page int, perPage int) ([]int, error) {
		return nil, sentinelError
	})
	if !errors.Is(err, sentinelError) {
		t.Errorf("FetchPaginated: expected error %v; got %v", sentinelError, err)
	}
}
