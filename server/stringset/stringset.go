package stringset

import "slices"

type Set[E string] map[E]struct{}

type StringSet = Set[string]

var element = struct{}{}

func FromSlice[E string](in []E) Set[E] {
	out := make(Set[E], len(in))
	for _, s := range in {
		out[s] = element
	}
	return out
}

func New[E string](in ...E) Set[E] { return FromSlice(in) }

func (s Set[E]) Add(ks ...E) {
	for _, k := range ks {
		s[k] = element
	}
}

func (s Set[E]) Remove(ks ...E) {
	for _, k := range ks {
		delete(s, k)
	}
}

func (s Set[E]) Contains(k E) bool {
	_, ok := s[k]
	return ok
}

func (s Set[E]) Equal(s2 Set[E]) bool {
	if len(s) != len(s2) {
		return false
	}
	if len(s.Union(s2)) != len(s) {
		return false
	}
	return true
}

// Difference returns a new set, containing only the elements that belong to both s and s2.
func (s Set[E]) Difference(s2 Set[E]) Set[E] {
	out := make(Set[E])
	for k := range s {
		if _, ok := s2[k]; !ok {
			out[k] = element
		}
	}
	return out
}

// SymmetricDifference returns the set of elements that belong to either s or s2, but not both.
func (s Set[E]) SymmetricDifference(s2 Set[E]) Set[E] {
	out := make(Set[E])
	for k := range s {
		if _, ok := s2[k]; !ok {
			out[k] = element
		}
	}
	for k := range s2 {
		if _, ok := s[k]; !ok {
			out[k] = element
		}
	}
	return out
}

// Union returns a new set, containing elements that belong to s or s2.
func (s Set[E]) Union(s2 Set[E]) Set[E] {
	out := make(Set[E])
	for k := range s {
		out[k] = element
	}
	for k := range s2 {
		out[k] = element
	}
	return out
}

// Intersection returns the set of elements that are in both s and s2.
func (s Set[E]) Intersection(s2 Set[E]) Set[E] {
	out := make(Set[E])
	for k := range s {
		if _, ok := s2[k]; ok {
			out[k] = element
		}
	}
	return out
}

// Sorted returns a sorted list of the strings in the set.
func (s Set[E]) Sorted() []E {
	els := make([]E, 0, len(s))
	for k := range s {
		els = append(els, k)
	}
	slices.Sort(els)
	return els
}
