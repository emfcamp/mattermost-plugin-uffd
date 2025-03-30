package paginator

import "fmt"

func FetchPaginated[T any](perPage int, fetchPage func(page, perPage int) ([]T, error)) ([]T, error) {
	var out []T
	page := 0
	for {
		us, err := fetchPage(page, perPage)
		if err != nil {
			return nil, fmt.Errorf("fetching page %d: %w", page, err)
		}
		out = append(out, us...)
		page++
		if len(us) < perPage {
			break
		}
	}
	return out, nil
}
